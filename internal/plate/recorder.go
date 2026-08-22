package plate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// Recorder is the interface a live recording uses: session.Writer.Open creates one, forwards
// every frame to AppendFrame, and calls Finalize or Abort at the end. Nothing outside this
// package sees batches, segment filenames or FFmpeg arguments.
//
// Every exported method is called from the same goroutine (internal/host.Run's single message
// loop), except for the one background encoder goroutine this type owns — so the only state
// that needs synchronization is the job queue and the sticky first-error flag it reports back.
type Recorder struct {
	dir         string
	framesDir   string
	segmentsDir string
	logf        func(format string, args ...any)

	// Owned exclusively by the caller goroutine (AppendFrame/Finalize/Abort never run
	// concurrently with each other).
	frameSeq     int
	segSeq       int
	haveFrame    bool
	lastFrameMs  int64
	batchStartMs int64
	batch        []frameRef

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []batchJob
	draining bool
	firstErr error
	degraded bool

	workerDone chan struct{}
}

// Open starts a Recorder rooted at dir, which must already exist (session.Writer creates it).
// It creates frames/ and segments/ and starts the single background encoder goroutine.
func Open(dir string, logf func(format string, args ...any)) (*Recorder, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve session dir: %w", err)
	}
	framesDir := framesDirFor(absDir)
	segmentsDir := segmentsDirFor(absDir)
	if err := os.MkdirAll(framesDir, 0o750); err != nil { // #nosec G301
		return nil, fmt.Errorf("create frames directory: %w", err)
	}
	if err := os.MkdirAll(segmentsDir, 0o750); err != nil { // #nosec G301
		return nil, fmt.Errorf("create segments directory: %w", err)
	}

	r := &Recorder{
		dir:         absDir,
		framesDir:   framesDir,
		segmentsDir: segmentsDir,
		logf:        logf,
		workerDone:  make(chan struct{}),
	}
	r.cond = sync.NewCond(&r.mu)
	go r.worker()
	return r, nil
}

// AppendFrame saves a captured frame to frames/ and, once ~20s of session-time has
// accumulated since the current batch's start, hands the closed batch to the background
// encoder. Frames must arrive in non-decreasing tMs, as the extension already guarantees.
func (r *Recorder) AppendFrame(tMs int64, data []byte) error {
	// CDP screencast timestamps can jitter backward by a few milliseconds between frames —
	// observed in production, not just a theoretical edge case. Treating that as a hard
	// failure aborted otherwise-fine recordings, so it's clamped to the previous frame's tMs
	// instead: a zero-length hold that the concat list's minFrameSec floor already handles,
	// not a reason to lose the rest of the capture.
	if r.haveFrame && tMs < r.lastFrameMs {
		r.logf("plate: frame tMs %d arrived before previous %d, clamping", tMs, r.lastFrameMs)
		tMs = r.lastFrameMs
	}
	r.frameSeq++
	path := filepath.Join(r.framesDir, frameFileName(r.frameSeq, tMs))
	// Written directly, not via a .part+rename dance: at up to 60 frames/second that rename
	// was a second syscall per frame for a durability guarantee this specific write barely
	// needs — a crash mid-write can only ever land on the batch's newest, not-yet-encoded
	// frame (every earlier frame in the batch is already fully on disk, or the batch would
	// not have been able to close past it), and recoverMissingSegments already treats a batch
	// whose encode fails as "keep the source JPEGs, try again later" rather than as fatal.
	// Measured in production: this process gets killed by macOS's per-process disk-I/O rate
	// governor (kernel "excessive I/O" / symptomsd disk-writes-limit) during real recordings,
	// not by anything an extra fsync-adjacent syscall was protecting against.
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G306
		return fmt.Errorf("write frame: %w", err)
	}
	ref := frameRef{tMs: tMs, path: path}
	r.lastFrameMs = tMs

	if !r.haveFrame {
		r.haveFrame = true
		// The whole plate starts at session time 0 regardless of when the first frame
		// actually painted, mirroring internal/render/plate.go's BuildConcatList i==0 case.
		r.batchStartMs = 0
		r.batch = []frameRef{ref}
		return nil
	}

	if tMs-r.batchStartMs >= windowMs {
		// ref crosses the threshold: it closes the current batch (without joining it) and
		// opens the next one, so every frame belongs to exactly one batch and no image is
		// ever duplicated across a batch boundary.
		r.enqueueBatch(r.batchStartMs, tMs, r.batch)
		r.batchStartMs = tMs
		r.batch = []frameRef{ref}
		return nil
	}
	r.batch = append(r.batch, ref)
	return nil
}

func (r *Recorder) enqueueBatch(startMs, endMs int64, frames []frameRef) {
	r.segSeq++
	job := batchJob{seq: r.segSeq, startMs: startMs, endMs: endMs, frames: frames}
	r.mu.Lock()
	r.queue = append(r.queue, job)
	r.cond.Signal()
	r.mu.Unlock()
}

// worker is the single background encoder goroutine. It never blocks AppendFrame: the queue
// is unbounded, so a slow encode just lets batches (and their JPEGs) pile up rather than
// stalling native-message intake.
func (r *Recorder) worker() {
	defer close(r.workerDone)
	for {
		r.mu.Lock()
		for len(r.queue) == 0 && !r.draining {
			r.cond.Wait()
		}
		if len(r.queue) == 0 {
			r.mu.Unlock()
			return
		}
		job := r.queue[0]
		r.queue = r.queue[1:]
		degraded := r.degraded
		r.mu.Unlock()

		// After the first encode failure, stop trying for the rest of the recording: capture
		// keeps running and keeps its JPEGs (nothing is deleted), and Finalize gets one
		// retry instead of hammering a broken FFmpeg on every batch.
		if degraded {
			continue
		}
		if err := r.encodeJob(job); err != nil {
			r.mu.Lock()
			if r.firstErr == nil {
				r.firstErr = err
				r.logf("plate: segment encode failed, keeping source frames for recovery: %s", err)
			}
			r.degraded = true
			r.mu.Unlock()
			continue
		}
		for _, f := range job.frames {
			_ = os.Remove(f.path) //nolint:errcheck // best-effort; a leftover JPEG only costs disk space
		}
	}
}

// encodeJob runs encodeBatch with panic recovery (via withRecover). This goroutine is the only
// place capture keeps running after AppendFrame returns, invisibly to session.Writer and
// internal/host — an unrecovered panic here would crash the entire process (Go terminates the
// whole program on any goroutine's unrecovered panic, not just this one), taking down
// native-message intake and losing whatever hadn't reached debug.log yet, mid-recording, for a
// failure scoped to one segment's encode. A bug here must degrade this recording's plate
// assembly like any other encode error, not end it.
func (r *Recorder) encodeJob(job batchJob) error {
	return withRecover(func() error { return encodeBatch(r.segmentsDir, job) })
}

// withRecover runs f and turns a panic into an error carrying the stack trace, instead of
// letting it unwind past the caller.
func withRecover(f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	return f()
}

// Stop tells the recorder to accept no more frames and returns immediately — it does not wait
// for the background encoder to finish whatever it's mid-processing, close the still-open
// batch, or attempt to assemble raw.mp4. Building raw.mp4 is unconditionally EnsureRaw's job
// now, run later from the detached post-production process, not this one.
//
// This used to be two methods, Finalize and Abort, and Finalize built raw.mp4 inline before
// returning. That is exactly what cost real recordings their finalize window: Chrome gives a
// disconnected native-messaging host only a few seconds to exit on its own before killing it
// outright ("Once the Port is disconnected the browser will give the process a few seconds to
// exit gracefully, and then kill it if it has not exited" — Chromium's own native-messaging
// docs), and a background worker with even one ~20s batch still queued can easily exceed that.
// Recordings were dying mid-Finalize with zero trace: no signal caught (it isn't one), no
// panic, no "aborted:" line (Abort's path, not this one), no "finalized:" line either (this
// one didn't get to finish). Whatever the worker is mid-encoding when the process actually
// exits is simply abandoned — its segment stays uncommitted, its source JPEGs stay on disk
// untouched — and EnsureRaw re-encodes it later from those same JPEGs, exactly as it already
// does for a genuine crash. Finalize and Abort collapsed into this one method because, once
// assembly moved out, there was nothing left to tell them apart: both just stop the recorder.
func (r *Recorder) Stop() {
	r.mu.Lock()
	r.draining = true
	r.cond.Signal()
	r.mu.Unlock()
}

func (r *Recorder) stopWorker() {
	r.mu.Lock()
	r.draining = true
	r.cond.Signal()
	r.mu.Unlock()
	<-r.workerDone
}
