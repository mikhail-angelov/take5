package plate

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// checkContiguous verifies committed segments cover [0,durationMs) with no gaps or overlaps.
// It never returns a truncated plate silently — a missing interval always surfaces as an
// error naming it.
func checkContiguous(segments []segmentInfo, durationMs int64) error {
	if len(segments) == 0 {
		return fmt.Errorf("missing plate coverage: no committed segments")
	}
	if segments[0].startMs != 0 {
		return fmt.Errorf("missing plate coverage: no segment starts at 0ms (first starts at %dms)", segments[0].startMs)
	}
	for i := 1; i < len(segments); i++ {
		if segments[i].startMs != segments[i-1].endMs {
			return fmt.Errorf("missing plate coverage: gap between %dms and %dms", segments[i-1].endMs, segments[i].startMs)
		}
	}
	last := segments[len(segments)-1]
	if last.endMs < durationMs {
		return fmt.Errorf("missing plate coverage: no segment reaches %dms (last ends at %dms)", durationMs, last.endMs)
	}
	return nil
}

// recoverMissingSegments fills every gap between committed segments (and up to durationMs)
// from whatever timestamped JPEGs survive in framesDir, encoding one segment per gap and
// deleting its source frames on success. Committed segments are always contiguous by
// construction (the single live worker processes batches in order), so in practice there is
// at most one gap — at the tail — but this stays generic so it also recovers a session that
// never ran the segmented encoder at all (framesDir full, segmentsDir empty).
func recoverMissingSegments(framesDir, segmentsDir string, durationMs int64) error {
	segments, err := listCommittedSegments(segmentsDir)
	if err != nil {
		return err
	}
	frames, err := listFrames(framesDir)
	if err != nil {
		return err
	}

	nextSeq := 0
	for _, s := range segments {
		if s.seq >= nextSeq {
			nextSeq = s.seq + 1
		}
	}

	type gap struct{ start, end int64 }
	var gaps []gap
	cursor := int64(0)
	for _, s := range segments {
		if s.startMs > cursor {
			gaps = append(gaps, gap{cursor, s.startMs})
		}
		cursor = s.endMs
	}
	if cursor < durationMs {
		gaps = append(gaps, gap{cursor, durationMs})
	}

	for _, g := range gaps {
		var batch []frameRef
		for _, f := range frames {
			if f.tMs >= g.start && f.tMs < g.end {
				batch = append(batch, f)
			}
		}
		if len(batch) == 0 {
			return fmt.Errorf("no source frames survived to rebuild [%dms,%dms)", g.start, g.end)
		}
		job := batchJob{seq: nextSeq, startMs: g.start, endMs: g.end, frames: batch}
		nextSeq++
		if err := encodeBatch(segmentsDir, job); err != nil {
			return err
		}
		for _, f := range batch {
			_ = os.Remove(f.path) //nolint:errcheck // best-effort; a leftover JPEG only costs disk space
		}
	}
	return nil
}

// assembleFromSegments concatenates every committed segment (stream copy, no video re-encode),
// muxes in voicePath as AAC if given, trims to durationMs, and atomically publishes
// dir/RawFile. It is the single place that turns committed segments into raw.mp4, used both
// right after a live recording (Recorder.Finalize) and by EnsureRaw's recovery path — so a
// recovered raw.mp4 is built exactly the same way a normal one is.
func assembleFromSegments(dir, framesDir, segmentsDir string, durationMs int64, voicePath string, logf func(format string, args ...any)) (string, error) {
	segments, err := listCommittedSegments(segmentsDir)
	if err != nil {
		return "", err
	}
	if err := checkContiguous(segments, durationMs); err != nil {
		return "", err
	}

	var concat strings.Builder
	concat.WriteString("ffconcat version 1.0\n")
	for _, s := range segments {
		fmt.Fprintf(&concat, "file '%s'\n", s.path)
	}
	concatPath := filepath.Join(segmentsDir, ".final.concat")
	if err := os.WriteFile(concatPath, []byte(concat.String()), 0o600); err != nil { // #nosec G306
		return "", fmt.Errorf("write final concat list: %w", err)
	}

	rawPath := filepath.Join(dir, RawFile)
	rawPartPath := rawPath + ".part"
	hasVoice := voicePath != ""

	args := []string{"-y", "-f", "concat", "-safe", "0", "-i", concatPath}
	if hasVoice {
		args = append(args, "-i", voicePath)
	}
	args = append(args,
		"-t", formatFixedKeep(float64(durationMs)/1000, 3),
		"-map", "0:v:0",
		"-c:v", "copy",
	)
	if hasVoice {
		// AAC, not the source webm/opus — raw.mp4 is an MPEG-4 container and Opus-in-MP4
		// isn't reliably supported by players that otherwise handle H.264/AAC fine.
		args = append(args, "-map", "1:a:0", "-c:a", "aac", "-b:a", "160k")
	}
	// Explicit container: the output path ends in .part, not .mp4.
	args = append(args, "-f", "mp4", rawPartPath)

	if err := runFfmpeg(args); err != nil {
		return "", fmt.Errorf("assemble raw.mp4: %w", err)
	}
	if err := probeVideo(rawPartPath); err != nil {
		_ = os.Remove(rawPartPath) //nolint:errcheck // best-effort cleanup of a rejected candidate
		return "", fmt.Errorf("assembled raw.mp4 failed validation: %w", err)
	}
	if err := os.Rename(rawPartPath, rawPath); err != nil {
		return "", fmt.Errorf("publish raw.mp4: %w", err)
	}

	_ = os.Remove(concatPath) //nolint:errcheck // best-effort scratch file cleanup
	// Only reached once raw.mp4 is a validated, complete source for frames/ and segments/:
	// nothing above this line deletes anything.
	removeIfExists(framesDir, logf)
	removeIfExists(segmentsDir, logf)
	return rawPath, nil
}

// removeIfExists is a best-effort os.RemoveAll: a stray frames/ or segments/ leftover only
// costs disk space, and must never turn a successfully published raw.mp4 into a failure.
func removeIfExists(dir string, logf func(format string, args ...any)) {
	if err := os.RemoveAll(dir); err != nil {
		logf("plate: could not remove %s: %s", dir, err)
	}
}

type sessionDurationDoc struct {
	DurationMs int64 `json:"durationMs"`
}

// readSessionDurationMs reads session.json's durationMs. found is false only when session.json
// itself does not exist yet — a session killed hard enough that session.Writer never reached
// its own Abort/Finalize (observed in production: no clean shutdown, so no "aborted:" line in
// debug.log either). A session.json that exists but fails to parse is a real error, not this
// case — recovery should not guess past corrupt metadata.
func readSessionDurationMs(dir string) (durationMs int64, found bool, err error) {
	// dir is a caller-provided session directory, not attacker-controlled input — the same
	// trust level internal/postproduction.Analyze already reads session.json under.
	buf, readErr := os.ReadFile(filepath.Join(dir, "session.json")) //nolint:gosec // G304
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read session.json: %w", readErr)
	}
	var doc sessionDurationDoc
	if err := json.Unmarshal(buf, &doc); err != nil {
		return 0, false, fmt.Errorf("could not parse session.json: %w", err)
	}
	return doc.DurationMs, true, nil
}

// inferDurationMs rebuilds a plausible durationMs from whatever plate sources survive when
// session.json itself never got written — the last committed segment's endMs, or the last
// surviving frame's tMs plus one frame's worth of hold (mirroring the pre-plate
// internal/render/plate.go's PlateDurationMs, which added the same buffer so the final frame
// isn't a zero-length interval). This can't recover events/network/session metadata — that
// trail is genuinely gone — but the video plate itself doesn't need it.
func inferDurationMs(framesDir, segmentsDir string) (int64, error) {
	segments, err := listCommittedSegments(segmentsDir)
	if err != nil {
		return 0, err
	}
	frames, err := listFrames(framesDir)
	if err != nil {
		return 0, err
	}
	if len(segments) == 0 && len(frames) == 0 {
		return 0, fmt.Errorf("no session.json, segments, or frames to recover a duration from")
	}
	var inferred int64
	if len(segments) > 0 {
		inferred = segments[len(segments)-1].endMs
	}
	if len(frames) > 0 {
		lastFrameEnd := int64(math.Ceil(float64(frames[len(frames)-1].tMs) + minFrameSec*1000))
		if lastFrameEnd > inferred {
			inferred = lastFrameEnd
		}
	}
	return inferred, nil
}

// EnsureRaw returns dir's validated raw.mp4, building or repairing it first if needed: a
// valid raw.mp4 is returned untouched (idempotent — a second render never re-assembles the
// plate); otherwise committed segments are read from segments/*.mp4, any gap up to durationMs
// is rebuilt from surviving frames/*.jpg, and the result is assembled and published exactly as
// Recorder.Finalize does. durationMs normally comes from session.json; if a recording was
// killed hard enough that session.Writer never got to write it at all (observed in
// production — no clean Abort, so no session.json and no "aborted:" line in debug.log either),
// it's inferred from the surviving segments/frames instead — the video plate can still be
// recovered even though the events/session metadata trail is genuinely gone. If a gap can't be
// rebuilt because its source frames are gone too, EnsureRaw fails loudly naming the missing
// interval rather than publishing a truncated video.
func EnsureRaw(dir string, logf func(format string, args ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve session dir: %w", err)
	}
	rawPath := filepath.Join(absDir, RawFile)
	if _, statErr := os.Stat(rawPath); statErr == nil {
		if probeErr := probeVideo(rawPath); probeErr == nil {
			// A crash between raw.mp4's rename and assembleFromSegments' own cleanup can
			// leave frames/segments behind next to an otherwise-valid raw.mp4; sweep them
			// here too so a stale session doesn't keep its stray sources forever.
			removeIfExists(framesDirFor(absDir), logf)
			removeIfExists(segmentsDirFor(absDir), logf)
			return rawPath, nil
		}
	}

	framesDir := framesDirFor(absDir)
	segmentsDir := segmentsDirFor(absDir)
	if mkdirErr := os.MkdirAll(segmentsDir, 0o750); mkdirErr != nil { // #nosec G301
		return "", fmt.Errorf("create segments directory: %w", mkdirErr)
	}

	durationMs, hasSession, err := readSessionDurationMs(absDir)
	if err != nil {
		return "", err
	}
	// internal/session.Writer checkpoints session.json on every event/network record now (a
	// separate production fix, for the same underlying reason as this one), not only at a
	// clean Finalize/Abort — so a killed recording usually does leave a session.json behind.
	// Its durationMs can still understate what's actually on disk: it's a snapshot as of the
	// last checkpoint, and frames keep arriving after the last event with no checkpoint of
	// their own (that would mean rewriting session.json up to 60x/s, for no benefit — frames
	// are already durable per-frame via this package). Trust segments/frames over a session.json
	// duration that's shorter than what they actually cover; never trust it over a longer one
	// (session.json genuinely knows about a trailing dead-air/hold stretch after capture that
	// left no frames, which inferDurationMs cannot see at all).
	inferred, inferErr := inferDurationMs(framesDir, segmentsDir)
	switch {
	case hasSession && inferErr == nil && inferred > durationMs:
		logf("plate: session.json duration %dms is shorter than recovered segments/frames (%dms); "+
			"using the longer value so nothing captured is silently dropped", durationMs, inferred)
		durationMs = inferred
	case !hasSession && inferErr == nil:
		logf("plate: session.json is missing (recording likely killed before it could be written); "+
			"inferring duration %dms from surviving segments/frames", inferred)
		durationMs = inferred
	case !hasSession && inferErr != nil:
		return "", fmt.Errorf("session.json is missing and could not be inferred: %w", inferErr)
	}

	if err := recoverMissingSegments(framesDir, segmentsDir, durationMs); err != nil {
		return "", fmt.Errorf("recover plate: %w", err)
	}

	voicePath := ""
	if _, err := os.Stat(filepath.Join(absDir, voiceFile)); err == nil {
		voicePath = filepath.Join(absDir, voiceFile)
	}
	return assembleFromSegments(absDir, framesDir, segmentsDir, durationMs, voicePath, logf)
}
