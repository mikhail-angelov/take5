package render

import (
	"os"
	"path/filepath"
	"testing"

	"take5/internal/director"
)

// Character-exact assertions, per seconds()/toFixed(6)'s own trap comment: a rounding
// disagreement only shows up as a diff in the literal string, not a numeric tolerance check.
// Values here were captured from the actual generator output and hand re-derived (frameMs =
// 1000/30 = 33.333333ms) before being pinned, not the other way round.

func TestPlanTemporalJobsProducesOneJobPerSegmentInOrder(t *testing.T) {
	timeline := []director.TimelineSegment{
		{SourceStartMs: 0, SourceEndMs: 1500, Speed: 1},
		{SourceStartMs: 1500, SourceEndMs: 1500, HoldMs: 400},
		{SourceStartMs: 1500, SourceEndMs: 5000, Speed: 1},
	}
	jobs, err := PlanTemporalJobs(timeline)
	if err != nil {
		t.Fatalf("PlanTemporalJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("len(jobs) = %d, want 3 (hold and speed segments must not drop out)", len(jobs))
	}
	for i, job := range jobs {
		if job.Index != i || job.Segment != timeline[i] {
			t.Errorf("jobs[%d] = %+v, want Index=%d Segment=%+v", i, job, i, timeline[i])
		}
	}
}

func TestPlanTemporalJobsRejectsAMalformedNonHeldSegment(t *testing.T) {
	timeline := []director.TimelineSegment{
		{SourceStartMs: 1000, SourceEndMs: 1000, Speed: 1}, // HoldMs == 0: a real, invalid, non-advancing segment
	}
	if _, err := PlanTemporalJobs(timeline); err == nil {
		t.Error("want an error for a non-held, non-advancing segment, got nil")
	}
}

func TestPlanTemporalJobsRejectsAnInvalidSpeedOnANonHeldSegment(t *testing.T) {
	timeline := []director.TimelineSegment{
		{SourceStartMs: 0, SourceEndMs: 1000, Speed: 0},
	}
	if _, err := PlanTemporalJobs(timeline); err == nil {
		t.Error("want an error for an invalid speed on a non-held segment, got nil")
	}
}

func TestPlanTemporalJobsRejectsAnEmptyTimeline(t *testing.T) {
	if _, err := PlanTemporalJobs(nil); err == nil {
		t.Error("want an error for an empty timeline, got nil")
	}
}

func TestTemporalSegmentArgsRendersAnOrdinarySegment(t *testing.T) {
	job := TemporalJob{Index: 0, Segment: director.TimelineSegment{SourceStartMs: 0, SourceEndMs: 1500, Speed: 1}}
	args := TemporalSegmentArgs("raw.mp4", job, 30, "out/000000.mp4.part")

	want := []string{
		"-y",
		"-i", "raw.mp4",
		"-filter_complex", "[0:v]fps=30,setpts=PTS-STARTPTS[src];\n" +
			"[src]trim=start=0.000000:end=1.500000,setpts=(PTS-STARTPTS)/1[out]",
		"-map", "[out]",
		"-r", "30",
		"-fps_mode", "cfr",
		"-t", "1.500",
		"-an",
		"-c:v", "libx264",
		"-preset", intermediatePreset,
		"-crf", "14",
		"-pix_fmt", "yuv420p",
		"-f", "mp4",
		"out/000000.mp4.part",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestTemporalSegmentArgsRendersAHeldSegmentAsAFrozenClip(t *testing.T) {
	job := TemporalJob{Index: 1, Segment: director.TimelineSegment{SourceStartMs: 1500, SourceEndMs: 1500, HoldMs: 400}}
	args := TemporalSegmentArgs("raw.mp4", job, 30, "out/000001.mp4.part")

	wantFilter := "[0:v]fps=30,setpts=PTS-STARTPTS[src];\n" +
		// One trimmed frame (1.500000 -> 1.533333, i.e. 1/30s) then tpad clones it for the
		// remaining 400 - 33.333333ms = 366.666667ms, not the full 400ms — the trimmed frame
		// itself already contributes one frame's worth of output duration.
		"[src]trim=start=1.500000:end=1.533333,setpts=PTS-STARTPTS,fps=30,tpad=stop_mode=clone:stop_duration=0.366667[out]"

	found := false
	for i, a := range args {
		if a == "-filter_complex" {
			found = true
			if args[i+1] != wantFilter {
				t.Errorf("filter =\n%s\nwant\n%s", args[i+1], wantFilter)
			}
		}
	}
	if !found {
		t.Fatal("no -filter_complex flag in args")
	}
}

func TestBuildTemporalConcatListFormatsOneFileLinePerClip(t *testing.T) {
	got := BuildTemporalConcatList([]string{"/tmp/000000.mp4", "/tmp/000001.mp4"})
	want := "ffconcat version 1.0\n" +
		"file '/tmp/000000.mp4'\n" +
		"file '/tmp/000001.mp4'\n"
	if got != want {
		t.Errorf("concat list = %q, want %q", got, want)
	}
}

// TestTemporalEndToEndAssemblesManySegmentsWithoutDroppingFrames is the regression test for
// the incident this rewrite fixes: BuildTemporalFilterGraph's old split+concat filter_complex
// silently dropped frames once a timeline reached the tens of segments (docs/plans/
// 20260822-action-model-and-robust-pass-a.md). A real recording after the typing-run fix
// rarely produces this many segments any more, but Pass A itself must stay correct regardless
// of segment count — this drives 56+ segments (the exact count observed in the incident
// recording) through the real Temporal() against a real FFmpeg source and checks the
// assembled clip's own duration, not just that the process exited zero.
func TestTemporalEndToEndAssemblesManySegmentsWithoutDroppingFrames(t *testing.T) {
	if !IsFfmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}

	dir := t.TempDir()
	writeRawVideo(t, filepath.Join(dir, "raw.mp4"), 12)

	const segments = 56
	const segMs = int64(150)
	timeline := make([]director.TimelineSegment, 0, segments)
	for i := range segments {
		start := int64(i) * segMs
		timeline = append(timeline, director.TimelineSegment{SourceStartMs: start, SourceEndMs: start + segMs, Speed: 1})
	}
	wantMs := int64(segments) * segMs

	if err := Temporal(dir, "raw.mp4", timeline, cleanFile, rawFPS); err != nil {
		t.Fatalf("Temporal: %v", err)
	}

	probe, err := ProbeVideo(filepath.Join(dir, cleanFile))
	if err != nil {
		t.Fatalf("ProbeVideo: %v", err)
	}
	if probe.DurationMs == nil {
		t.Fatal("probe reported no duration")
	}
	diff := *probe.DurationMs - wantMs
	if diff < 0 {
		diff = -diff
	}
	if diff > 200 {
		t.Errorf("assembled %d ms of video from %d segments, want %d ms (frames were dropped)", *probe.DurationMs, segments, wantMs)
	}

	if _, statErr := os.Stat(filepath.Join(dir, tmpDir, "pass-a-segments")); !os.IsNotExist(statErr) {
		t.Error("pass-a-segments scratch directory should have been removed after a successful assembly")
	}
}
