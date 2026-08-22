// Package plate owns the physical lifecycle of a session directory's video source: the
// timestamped JPEG frames a recording receives, the H.264 segments they get incrementally
// encoded into while capture is still running, and raw.mp4, the unedited plate the rest of
// the pipeline (internal/render) reads. session.Writer owns events, network and session.json;
// this package owns everything under frames/ and segments/, so a long recording's peak disk
// usage is bounded by the encode window rather than by the whole recording's frame count.
//
// docs/plans/2026-08-22-segmented-plate-recording.md is the design this ports.
package plate

import "path/filepath"

// RawFile is the filename of the assembled video plate, published only once verified.
// Duplicated as a literal in internal/director (director.go sets project.Source to "raw.mp4"
// directly) rather than imported from here, the same tolerance for a small duplicated
// filename constant the project already accepts elsewhere.
const RawFile = "raw.mp4"

// FramesDir is the directory holding not-yet-committed timestamped JPEG frames.
const FramesDir = "frames"

// SegmentsDir is the directory holding committed H.264 segments.
const SegmentsDir = "segments"

// voiceFile is internal/session's VoiceFile ("voice.webm"), duplicated here as a literal
// rather than imported: session.Writer holds a *Recorder, so this package importing session
// back would cycle. Both packages own the same string independently, as
// internal/render/ffmpeg.go's resolveBin duplication across exec-wrapper packages already
// does for a different reason (SPIKE.md §5).
const voiceFile = "voice.webm"

// windowMs is the batch size a live recording aims for before handing a batch to the
// background encoder. Not a hard bound: a batch only closes when a frame actually arrives to
// close it, so a long static pause becomes one longer hold-segment rather than several
// synthetic ones — see the design doc's "Семантика batch-накопления".
const windowMs int64 = 20000

// plateFPS and minFrameSec mirror internal/render/plate.go's rationale: Chrome emits a frame
// per composited update, so 60 keeps a scrolling capture intact without costing anything on a
// still page.
const plateFPS = 60
const minFrameSec = 1.0 / plateFPS

// frameRef is one committed-to-disk JPEG: its capture timestamp and where it lives.
type frameRef struct {
	tMs  int64
	path string
}

// batchJob is an immutable, already-closed range of frames ready for the background encoder
// (or, during recovery, for direct synchronous encoding).
type batchJob struct {
	seq     int
	startMs int64
	endMs   int64
	frames  []frameRef
}

// segmentInfo is a committed segment file's identity, parsed back out of its filename — the
// filename is the only state a segment carries; there is no separate manifest.
type segmentInfo struct {
	seq     int
	startMs int64
	endMs   int64
	path    string
}

func framesDirFor(dir string) string   { return filepath.Join(dir, FramesDir) }
func segmentsDirFor(dir string) string { return filepath.Join(dir, SegmentsDir) }
