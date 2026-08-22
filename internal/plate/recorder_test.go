package plate

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func ffmpegAvailable() bool {
	return runFfmpeg([]string{"-version"}) == nil
}

func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 30, G: 120, B: 200, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeSessionJSON(t *testing.T, dir string, durationMs int64) {
	t.Helper()
	doc := map[string]any{"version": 2, "durationMs": durationMs}
	buf, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAppendFrameToleratesOutOfOrderTimestamps locks in a production fix: CDP screencast
// frames can arrive with tMs jittering a few milliseconds backward, and that must not abort
// an otherwise-fine recording (as a hard error here used to).
func TestAppendFrameToleratesOutOfOrderTimestamps(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.AppendFrame(100, makeJPEG(t)); err != nil {
		t.Fatal(err)
	}
	if err = r.AppendFrame(83, makeJPEG(t)); err != nil {
		t.Fatalf("AppendFrame with a slightly earlier tMs must not error, got: %v", err)
	}
	r.stopWorker()

	entries, err := os.ReadDir(filepath.Join(dir, FramesDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("frames/ has %d entries, want 2 (both frames must still be written)", len(entries))
	}
}

func TestStopPreservesRecoveryArtifactsWithoutBuildingRaw(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Both frames stay well under one batch's ~20s threshold, so nothing is ever handed to
	// the background encoder and this test needs no FFmpeg at all.
	if err = r.AppendFrame(0, makeJPEG(t)); err != nil {
		t.Fatal(err)
	}
	if err = r.AppendFrame(500, makeJPEG(t)); err != nil {
		t.Fatal(err)
	}
	r.stopWorker()

	entries, err := os.ReadDir(filepath.Join(dir, FramesDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("frames/ has %d entries after Stop, want 2 (Stop must not delete recovery material)", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, RawFile)); err == nil {
		t.Fatal("raw.mp4 must not exist after Stop — building it is EnsureRaw's job now")
	}
}

// TestStopReturnsImmediatelyRegardlessOfEncodeBacklog is the direct regression test for a
// production incident: Chrome gives a disconnected native-messaging host only a few seconds
// to exit before killing it outright (see Stop's doc comment for the full account, including
// the Chromium documentation quote). The old Finalize built raw.mp4 inline — draining the
// background encoder, then concatenating every segment — and a real recording died silently
// mid-Finalize because that routinely took longer than Chrome's grace period. No test caught
// it beforehand because every existing test asserted *what* Finalize returned, never *how
// long it took under load* — a correctness property, not the latency property that actually
// mattered here. This test asserts latency directly: queue several batches' worth of real
// FFmpeg-encode work (enough that the single background worker cannot possibly have drained
// it yet) and then measure Stop, which must return in O(1) — a flag set and a condvar
// signaled — not in however long the backlog takes to encode.
func TestStopReturnsImmediatelyRegardlessOfEncodeBacklog(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	r, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// ~30 frames per ~21s span, 4 times over: each span crosses the ~20s batch threshold, so
	// this closes (and queues for the single background worker) several real segments' worth
	// of encode work — a worker that drains synchronously could not possibly keep up with
	// AppendFrame, which only writes a JPEG to disk per call.
	tMs := int64(0)
	for span := 0; span < 4; span++ {
		for i := 0; i < 30; i++ {
			if err := r.AppendFrame(tMs, makeJPEG(t)); err != nil {
				t.Fatalf("AppendFrame: %v", err)
			}
			tMs += 700
		}
	}

	start := time.Now()
	r.Stop()
	elapsed := time.Since(start)
	// Generous headroom over what Stop should ever take (well under a millisecond in
	// practice), and nowhere near Chrome's multi-second budget — this bound exists to fail
	// loudly and fast if Stop ever regresses into waiting on the worker again, not to be tight.
	if elapsed > 200*time.Millisecond {
		t.Fatalf("Stop took %s with a multi-segment encode backlog still queued; it must return "+
			"immediately regardless of backlog, not wait for the background worker", elapsed)
	}
}

// TestWithRecoverConvertsPanicToError guards the mechanism worker() relies on to survive a bug
// in encodeBatch without crashing the whole host process (Go terminates the entire program on
// any goroutine's unrecovered panic) — a production recording was lost outright once already,
// from an unrelated uncaught-signal cause; this closes the analogous risk on the encoder side.
func TestWithRecoverConvertsPanicToError(t *testing.T) {
	err := withRecover(func() error { panic("boom") })
	if err == nil {
		t.Fatal("expected a non-nil error, the panic must not propagate past withRecover")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to mention the panic value", err.Error())
	}
}

func TestWithRecoverPassesThroughAnOrdinaryError(t *testing.T) {
	want := errors.New("ordinary failure")
	if err := withRecover(func() error { return want }); !errors.Is(err, want) {
		t.Errorf("withRecover(...) = %v, want %v unchanged", err, want)
	}
}

// TestRecorderThenEnsureRawProducesRawMp4 exercises the current split: Stop only closes the
// recorder (fast, no assembly), and EnsureRaw — reading durationMs from session.json, the
// same way internal/postproduction.Render calls it — does the actual raw.mp4 assembly. This
// replaced a single Recorder.Finalize that did both inline; see Stop's doc comment for why.
func TestRecorderThenEnsureRawProducesRawMp4(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	r, err := Open(dir, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	// Crosses the ~20s threshold twice, so this recording commits two segments before the
	// still-open tail batch — exercising the background worker, not just the tail path.
	for _, tMs := range []int64{0, 5000, 12000, 21000, 25000, 41000} {
		if err = r.AppendFrame(tMs, makeJPEG(t)); err != nil {
			t.Fatalf("AppendFrame(%d): %v", tMs, err)
		}
	}
	r.stopWorker() // deterministic wait, in place of production's non-waiting Stop
	writeSessionJSON(t, dir, 45000)

	rawPath, err := EnsureRaw(dir, t.Logf)
	if err != nil {
		t.Fatalf("EnsureRaw: %v", err)
	}
	if rawPath != filepath.Join(dir, RawFile) {
		t.Errorf("rawPath = %q", rawPath)
	}
	if err := probeVideo(rawPath); err != nil {
		t.Errorf("published raw.mp4 does not probe as a video: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FramesDir)); !os.IsNotExist(err) {
		t.Errorf("frames/ still exists after a successful EnsureRaw (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, SegmentsDir)); !os.IsNotExist(err) {
		t.Errorf("segments/ still exists after a successful EnsureRaw (err=%v)", err)
	}
}

func TestEnsureRawIsIdempotentOnAnAlreadyValidRaw(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	writeSessionJSON(t, dir, 1000)
	rawPath := filepath.Join(dir, RawFile)
	if err := runFfmpeg([]string{
		"-y", "-f", "lavfi", "-i", "testsrc2=size=64x48:rate=30:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-an", rawPath,
	}); err != nil {
		t.Fatalf("seed raw.mp4: %v", err)
	}

	got, err := EnsureRaw(dir, t.Logf)
	if err != nil {
		t.Fatalf("EnsureRaw: %v", err)
	}
	if got != rawPath {
		t.Errorf("EnsureRaw returned %q, want %q", got, rawPath)
	}
	if _, err := os.Stat(filepath.Join(dir, SegmentsDir)); !os.IsNotExist(err) {
		t.Error("EnsureRaw must not create segments/ when raw.mp4 is already valid")
	}
}

func TestEnsureRawRecoversFromLeftoverFramesAfterACrash(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	writeSessionJSON(t, dir, 1500)
	framesDir := filepath.Join(dir, FramesDir)
	if err := os.MkdirAll(framesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for i, tMs := range []int64{0, 500, 1200} {
		if err := os.WriteFile(filepath.Join(framesDir, frameFileName(i+1, tMs)), makeJPEG(t), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rawPath, err := EnsureRaw(dir, t.Logf)
	if err != nil {
		t.Fatalf("EnsureRaw: %v", err)
	}
	if err := probeVideo(rawPath); err != nil {
		t.Errorf("recovered raw.mp4 does not probe as a video: %v", err)
	}
	if _, err := os.Stat(framesDir); !os.IsNotExist(err) {
		t.Errorf("frames/ still exists after recovery (err=%v)", err)
	}
}

func probeDurationMs(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command(ffprobeBin(), //nolint:gosec // test helper, path is a t.TempDir() file this test just wrote
		"-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	ms, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse ffprobe duration %q: %v", out, err)
	}
	return ms * 1000
}

// TestEnsureRawDoesNotTruncateBelowWhatSegmentsAndFramesCover covers a subtler production
// fix: internal/session.Writer now checkpoints session.json on every event, but that
// checkpoint's durationMs is only as fresh as the last event — frames keep arriving afterward
// with no checkpoint of their own. If a hard kill happens in that window, session.json's
// durationMs understates what's actually on disk, and EnsureRaw must not use it to silently
// trim real captured footage off the end of raw.mp4.
func TestEnsureRawDoesNotTruncateBelowWhatSegmentsAndFramesCover(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	// session.json claims 1000ms, but frames below go out to 2500ms — the stale-checkpoint
	// shape: the last event happened at 1000ms, and capture kept going a while after that.
	writeSessionJSON(t, dir, 1000)
	framesDir := filepath.Join(dir, FramesDir)
	if err := os.MkdirAll(framesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for i, tMs := range []int64{0, 800, 1600, 2500} {
		if err := os.WriteFile(filepath.Join(framesDir, frameFileName(i+1, tMs)), makeJPEG(t), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rawPath, err := EnsureRaw(dir, t.Logf)
	if err != nil {
		t.Fatalf("EnsureRaw: %v", err)
	}
	gotMs := probeDurationMs(t, rawPath)
	if gotMs < 2400 {
		t.Errorf("raw.mp4 duration = %.0fms, want ~2500ms (session.json's stale 1000ms must not truncate it)", gotMs)
	}
}

func TestEnsureRawFailsLoudlyWhenAGapHasNoSourceFrames(t *testing.T) {
	dir := t.TempDir()
	writeSessionJSON(t, dir, 1000)
	// No frames/, no segments/: nothing to rebuild [0ms,1000ms) from.
	if _, err := EnsureRaw(dir, nil); err == nil {
		t.Fatal("expected EnsureRaw to fail instead of publishing a truncated raw.mp4")
	}
	if _, err := os.Stat(filepath.Join(dir, RawFile)); !os.IsNotExist(err) {
		t.Error("raw.mp4 must not exist after a failed recovery")
	}
}

// TestEnsureRawRecoversWithoutSessionJSON covers a recording killed hard enough that
// session.Writer never reached Abort/Finalize at all — observed in production: committed
// segments and leftover tail frames survive, but there is no session.json to read durationMs
// from. EnsureRaw must still recover the plate by inferring duration from what's on disk.
func TestEnsureRawRecoversWithoutSessionJSON(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	// No writeSessionJSON call: session.json is deliberately absent, matching a hard kill
	// mid-recording.
	r, err := Open(dir, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	for _, tMs := range []int64{0, 5000, 12000, 21000, 25000} {
		if err = r.AppendFrame(tMs, makeJPEG(t)); err != nil {
			t.Fatalf("AppendFrame(%d): %v", tMs, err)
		}
	}
	// Stop must not write session.json (that's session.Writer's job) or build raw.mp4, only
	// let the background worker commit whatever segment it already closed (batch 1,
	// [0ms,21000ms)) and leave the rest as recovery material — the same shape a hard kill
	// right after that commit would leave.
	r.stopWorker()
	if _, statErr := os.Stat(filepath.Join(dir, "session.json")); !os.IsNotExist(statErr) {
		t.Fatalf("session.json must not exist for this test to be testing the right thing (err=%v)", statErr)
	}

	rawPath, err := EnsureRaw(dir, t.Logf)
	if err != nil {
		t.Fatalf("EnsureRaw: %v", err)
	}
	if err := probeVideo(rawPath); err != nil {
		t.Errorf("recovered raw.mp4 does not probe as a video: %v", err)
	}
}
