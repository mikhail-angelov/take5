package plate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// formatFixedKeep is toFixed without trailing-zero trimming, for FFmpeg's concat `duration`
// lines — a straight port of internal/render/numformat.go's helper of the same name, kept as
// its own three lines here rather than exported from render for one call site.
func formatFixedKeep(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

func frameFileName(seq int, tMs int64) string {
	return fmt.Sprintf("%06d-%012d.jpg", seq, tMs)
}

var frameNameRe = regexp.MustCompile(`^(\d{6})-(\d{12})\.jpg$`)

func parseFrameFileName(name string) (seq int, tMs int64, ok bool) {
	m := frameNameRe.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	seq64, err1 := strconv.ParseInt(m[1], 10, 64)
	t, err2 := strconv.ParseInt(m[2], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int(seq64), t, true
}

// listFrames scans framesDir for committed (non-.part) JPEGs and returns them ordered by
// sequence, which is also timestamp order — AppendFrame rejects out-of-order tMs.
func listFrames(framesDir string) ([]frameRef, error) {
	entries, err := os.ReadDir(framesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list frames: %w", err)
	}
	frames := make([]frameRef, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		seq, tMs, ok := parseFrameFileName(e.Name())
		if !ok {
			continue
		}
		frames = append(frames, frameRef{tMs: tMs, path: filepath.Join(framesDir, e.Name())})
		_ = seq
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].tMs < frames[j].tMs })
	return frames, nil
}

func segmentFileName(seq int, startMs, endMs int64) string {
	return fmt.Sprintf("%06d-%012d-%012d.mp4", seq, startMs, endMs)
}

var segmentNameRe = regexp.MustCompile(`^(\d{6})-(\d{12})-(\d{12})\.mp4$`)

func parseSegmentFileName(name string) (info segmentInfo, ok bool) {
	m := segmentNameRe.FindStringSubmatch(name)
	if m == nil {
		return segmentInfo{}, false
	}
	seq64, err1 := strconv.ParseInt(m[1], 10, 64)
	start, err2 := strconv.ParseInt(m[2], 10, 64)
	end, err3 := strconv.ParseInt(m[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return segmentInfo{}, false
	}
	return segmentInfo{seq: int(seq64), startMs: start, endMs: end}, true
}

// listCommittedSegments scans segmentsDir for committed (non-.part) segments, ordered by
// sequence number — the same order the live worker committed them in, and the order concat
// must play them back in.
func listCommittedSegments(segmentsDir string) ([]segmentInfo, error) {
	entries, err := os.ReadDir(segmentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list segments: %w", err)
	}
	segments := make([]segmentInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ok := parseSegmentFileName(e.Name())
		if !ok {
			continue
		}
		info.path = filepath.Join(segmentsDir, e.Name())
		segments = append(segments, info)
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].seq < segments[j].seq })
	return segments, nil
}

// buildConcatList generates FFmpeg concat demuxer syntax for one batch. frames[0] is held from
// startMs (the batch's own start, which for a live batch is always frames[0].tMs except for the
// session's very first batch, which starts the whole plate at 0 regardless of when the first
// frame actually painted — mirroring internal/render/plate.go's BuildConcatList i==0 case).
// Every later frame is held from its own tMs, and the last frame is held until endMs.
func buildConcatList(frames []frameRef, startMs, endMs int64) (string, error) {
	if len(frames) == 0 {
		return "", fmt.Errorf("empty batch")
	}
	lines := []string{"ffconcat version 1.0"}
	for i, f := range frames {
		start := f.tMs
		if i == 0 {
			start = startMs
		}
		next := endMs
		if i+1 < len(frames) {
			next = frames[i+1].tMs
		}
		secs := (float64(next) - float64(start)) / 1000
		if secs < minFrameSec {
			secs = minFrameSec
		}
		lines = append(lines,
			fmt.Sprintf("file '%s'", f.path),
			fmt.Sprintf("duration %s", formatFixedKeep(secs, 4)),
		)
	}
	// The concat demuxer ignores the final duration unless the last file is repeated.
	lines = append(lines, fmt.Sprintf("file '%s'", frames[len(frames)-1].path))
	return strings.Join(lines, "\n") + "\n", nil
}

// encodeBatch encodes one committed segment from job into segmentsDir: build the concat list,
// run FFmpeg into a .part file, then atomically rename it into place. Called from both the
// live background worker and crash/recovery paths (Finalize's one retry, EnsureRaw) — the
// filename itself is the commit; there is no separate manifest write.
func encodeBatch(segmentsDir string, job batchJob) error {
	concatList, err := buildConcatList(job.frames, job.startMs, job.endMs)
	if err != nil {
		return err
	}
	concatPath := filepath.Join(segmentsDir, fmt.Sprintf(".tmp-%06d.concat", job.seq))
	if err := os.WriteFile(concatPath, []byte(concatList), 0o600); err != nil { // #nosec G306
		return fmt.Errorf("write concat list: %w", err)
	}
	defer os.Remove(concatPath) //nolint:errcheck // best-effort scratch file cleanup

	name := segmentFileName(job.seq, job.startMs, job.endMs)
	partPath := filepath.Join(segmentsDir, name+".part")
	finalPath := filepath.Join(segmentsDir, name)
	durSec := float64(job.endMs-job.startMs) / 1000

	args := []string{
		"-y",
		"-f", "concat", "-safe", "0", "-i", concatPath,
		"-t", formatFixedKeep(durSec, 3),
		// Odd dimensions are possible at fractional device pixel ratios, and yuv420p needs
		// even; `fps` (not `-r`) so a still stretch — a gap in the concat timeline — is filled
		// by holding the previous frame rather than left short.
		"-vf", fmt.Sprintf("scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=%d", plateFPS),
		"-map", "0:v:0",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "18",
		// Explicit container: the output path ends in .part, not .mp4, so FFmpeg can't infer
		// the format from the extension.
		"-f", "mp4",
		partPath,
	}
	if err := runFfmpeg(args); err != nil {
		return fmt.Errorf("encode segment [%dms,%dms): %w", job.startMs, job.endMs, err)
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		return fmt.Errorf("commit segment [%dms,%dms): %w", job.startMs, job.endMs, err)
	}
	return nil
}
