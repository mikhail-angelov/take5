package render

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"take5/internal/director"
)

// Pass A — temporal edit (spec 22.1): trim the kept segments, retime the accelerated ones,
// concatenate. Nothing visual happens here.
//
// Each segment of project.Timeline is encoded by its own small FFmpeg process, independent of
// every other segment, then stitched together with the concat demuxer (`-c:v copy`, no
// re-encode) — the same component-encode-then-concat shape internal/plate already uses for
// raw.mp4 (internal/plate/segment.go's encodeBatch, internal/plate/finalize.go's
// assembleFromSegments). This replaces an earlier single `filter_complex` built from
// `split=N` + N×`trim` + `concat=n=N`: FFmpeg has to hold all N branches' decoded frames in
// flight at once for that graph to resolve (concat can't take branch 0 until its trim sees
// EOF from split, which split can't deliver until the whole source is read), so it silently
// dropped frames once a real recording's timeline reached the tens of segments — reproduced
// directly against the incident recording in docs/plans/20260822-action-model-and-robust-
// pass-a.md. Per-segment encoding has no such ceiling: N small processes cost more wall time
// than one large one, but the cost is linear in N, not a hidden coupling between every branch.

// Near-lossless intermediate so the second pass does not compound compression artifacts.
const intermediateCRF = 14
const intermediatePreset = "veryfast"

func seconds(ms int64) string {
	// toFixed(6), kept exactly — this is the trap flagged in docs/plans/go-port.md: the
	// string goes straight into the filter graph, so only a character-exact test catches
	// a rounding disagreement.
	return formatFixedKeep(float64(ms)/1000, 6)
}

// TemporalJob is one timeline segment resolved into a Pass A encode job. Index is the
// segment's position in project.Timeline, which also names the segment's own clip file so the
// concat demuxer plays every clip back in timeline order.
type TemporalJob struct {
	Index   int
	Segment director.TimelineSegment
}

// PlanTemporalJobs turns a timeline into one job per segment. project.json may have been
// hand-edited; a segment that is neither a valid held (freeze) segment nor a valid advancing,
// positive-speed segment is refused loudly here rather than handed to FFmpeg, which would
// otherwise mangle it silently — the same guard BuildTemporalFilterGraph used to apply before
// building its single graph.
func PlanTemporalJobs(timeline []director.TimelineSegment) ([]TemporalJob, error) {
	if len(timeline) == 0 {
		return nil, fmt.Errorf("timeline is empty: nothing to render")
	}
	jobs := make([]TemporalJob, 0, len(timeline))
	for i, segment := range timeline {
		if segment.HoldMs > 0 {
			jobs = append(jobs, TemporalJob{Index: i, Segment: segment})
			continue
		}
		if segment.Speed <= 0 {
			return nil, fmt.Errorf("timeline segment %d has an invalid speed: %v", i, segment.Speed)
		}
		if segment.SourceEndMs <= segment.SourceStartMs {
			return nil, fmt.Errorf("timeline segment %d does not advance: %d -> %d", i, segment.SourceStartMs, segment.SourceEndMs)
		}
		jobs = append(jobs, TemporalJob{Index: i, Segment: segment})
	}
	return jobs, nil
}

// segmentFilterChain emits one segment's own filter line, reading from [inLabel] and writing
// to [outLabel]. A held segment gets a single trimmed frame at its source instant, held for
// the requested output duration by tpad; every other segment keeps the ordinary
// trim+setpts/speed retiming.
func segmentFilterChain(inLabel string, segment director.TimelineSegment, fps int, outLabel string) string {
	if segment.HoldMs <= 0 {
		return fmt.Sprintf(
			"[%s]trim=start=%s:end=%s,setpts=(PTS-STARTPTS)/%s[%s]",
			inLabel, seconds(segment.SourceStartMs), seconds(segment.SourceEndMs), speedString(segment.Speed), outLabel,
		)
	}

	// The trimmed window itself already contributes one frame's worth of output duration, so
	// the clone pad only needs to cover the remainder — using HoldMs directly here would make
	// the rendered segment one frame longer than the timeline claims.
	frameMs := 1000.0 / float64(fps)
	padMs := float64(segment.HoldMs) - frameMs
	if padMs < 0 {
		padMs = 0
	}
	// tpad's stop_duration converts to a frame count using the incoming link's own frame
	// rate; a single-frame stream coming straight out of trim carries no inferable rate of
	// its own (there's only one frame — no consecutive delta to measure), so tpad silently
	// pads zero frames without a second, explicit fps= re-asserting it right beforehand.
	return fmt.Sprintf(
		"[%s]trim=start=%s:end=%s,setpts=PTS-STARTPTS,fps=%d,tpad=stop_mode=clone:stop_duration=%s[%s]",
		inLabel,
		seconds(segment.SourceStartMs),
		formatFixedKeep(float64(segment.SourceStartMs)/1000+frameMs/1000, 6),
		fps,
		formatFixedKeep(padMs/1000, 6),
		outLabel,
	)
}

// speedString mirrors JS's bare template interpolation of project.json's `speed` (already
// rounded to 4dp by the Director) — default number-to-string, not toFixed.
func speedString(speed float64) string {
	return strconv.FormatFloat(speed, 'f', -1, 64)
}

// segmentDurationMs is how much output-time a segment occupies — the same arithmetic
// buildTimeline uses per merged segment (HoldMs directly, or the retimed span otherwise).
func segmentDurationMs(segment director.TimelineSegment) float64 {
	if segment.HoldMs > 0 {
		return float64(segment.HoldMs)
	}
	return float64(segment.SourceEndMs-segment.SourceStartMs) / segment.Speed
}

// TemporalSegmentArgs returns FFmpeg arguments to encode one segment (job) of source into
// outPath, independent of every other segment — the same trim/setpts/tpad arithmetic
// segmentFilterChain has always used, just run as its own small filter graph instead of one
// branch of a single graph spanning every segment in the timeline. Reading from the start of
// source every time (no `-ss`) rather than seeking is deliberate: it reproduces the exact
// frame-accurate trim points the single-graph version used to produce, at the cost of a fuller
// decode per segment — acceptable since each encode is now its own small process rather than
// one that has to hold the whole timeline in flight. `-t` bounds this segment's own clip to
// its planned duration: normalising to CFR independently per segment (rather than once across
// the whole timeline, as the single-graph version did) can round a fractional frame up at a
// segment's own trim boundary, and left unbounded that rounding compounds across many small
// clips into a drift the post-Pass-A duration guard would otherwise have to absorb.
func TemporalSegmentArgs(source string, job TemporalJob, fps int, outPath string) []string {
	filter := fmt.Sprintf("[0:v]fps=%d,setpts=PTS-STARTPTS[src];\n%s", fps, segmentFilterChain("src", job.Segment, fps, "out"))
	return []string{
		"-y",
		"-i", source,
		"-filter_complex", filter,
		"-map", "[out]",
		"-r", strconv.Itoa(fps),
		"-fps_mode", "cfr",
		"-t", formatFixedKeep(segmentDurationMs(job.Segment)/1000, 3),
		// Audio is out of scope for the MVP (spec 23).
		"-an",
		"-c:v", "libx264",
		"-preset", intermediatePreset,
		"-crf", strconv.Itoa(intermediateCRF),
		"-pix_fmt", "yuv420p",
		"-f", "mp4",
		outPath,
	}
}

// BuildTemporalConcatList generates FFmpeg concat demuxer syntax for a set of already-encoded
// segment clips — each clip already carries its own exact duration, unlike
// internal/plate's JPEG concat lists, so no `duration` directive is needed per entry, a direct
// analogy to internal/plate/finalize.go's assembleFromSegments own final concat list for
// raw.mp4.
func BuildTemporalConcatList(clipPaths []string) string {
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, p := range clipPaths {
		fmt.Fprintf(&b, "file '%s'\n", p)
	}
	return b.String()
}

// temporalDurationMs sums a timeline's own output duration exactly the way director's
// outputDurationMs does, so the concat demuxer's `-t` bound matches project.DurationMs and
// per-segment encoder rounding drift can't leak an extra frame or two into clean.mp4.
func temporalDurationMs(timeline []director.TimelineSegment) int64 {
	total := int64(0)
	for _, s := range timeline {
		if s.HoldMs > 0 {
			total += s.HoldMs
			continue
		}
		if s.Speed <= 0 {
			continue
		}
		total += int64(math.Round(float64(s.SourceEndMs-s.SourceStartMs) / s.Speed))
	}
	return total
}

// Temporal executes Pass A: one small FFmpeg encode per timeline segment into
// dir/.tmp/pass-a-segments/, then a concat-demuxer stream copy into dir/output. Segment clips
// and the concat list are removed once output is published; a failure partway leaves them
// behind for diagnosis, same as every other intermediate under dir/.tmp (spec 10).
func Temporal(dir, source string, timeline []director.TimelineSegment, output string, fps int) error {
	jobs, err := PlanTemporalJobs(timeline)
	if err != nil {
		return err
	}

	segmentsDir := filepath.Join(dir, tmpDir, "pass-a-segments")
	if mkdirErr := os.MkdirAll(segmentsDir, 0o750); mkdirErr != nil { // #nosec G301
		return fmt.Errorf("create pass A segments directory: %w", mkdirErr)
	}

	clipPaths := make([]string, len(jobs))
	for i, job := range jobs {
		finalPath := filepath.Join(segmentsDir, fmt.Sprintf("%06d.mp4", job.Index))
		partPath := finalPath + ".part"
		if runErr := RunFfmpeg(TemporalSegmentArgs(source, job, fps, partPath), dir); runErr != nil {
			return fmt.Errorf("encode pass A segment %d: %w", job.Index, runErr)
		}
		if renameErr := os.Rename(partPath, finalPath); renameErr != nil {
			return fmt.Errorf("commit pass A segment %d: %w", job.Index, renameErr)
		}
		clipPaths[i] = finalPath
	}

	concatPath := filepath.Join(segmentsDir, ".concat")
	if writeErr := os.WriteFile(concatPath, []byte(BuildTemporalConcatList(clipPaths)), 0o600); writeErr != nil { // #nosec G306
		return fmt.Errorf("write pass A concat list: %w", writeErr)
	}

	outputPath := filepath.Join(dir, output)
	outputPartPath := outputPath + ".part"
	concatArgs := []string{
		"-y",
		"-f", "concat", "-safe", "0", "-i", concatPath,
		"-t", formatFixedKeep(float64(temporalDurationMs(timeline))/1000, 3),
		"-c:v", "copy",
		"-f", "mp4",
		outputPartPath,
	}
	if runErr := RunFfmpeg(concatArgs, ""); runErr != nil {
		return fmt.Errorf("concat pass A segments: %w", runErr)
	}
	if renameErr := os.Rename(outputPartPath, outputPath); renameErr != nil {
		return fmt.Errorf("publish pass A output: %w", renameErr)
	}

	if rmErr := os.RemoveAll(segmentsDir); rmErr != nil {
		return fmt.Errorf("remove pass A segment scratch files: %w", rmErr)
	}
	return nil
}
