package render

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"take5/internal/director"
)

// Compiles a project.json into demo.mp4 through the two FFmpeg passes (spec 22).
// Direct port of src/render/index.js.

const tmpDir = ".tmp"
const cleanFile = tmpDir + "/clean.mp4"
const passBFilter = tmpDir + "/pass-b.filter"
const assFileName = tmpDir + "/overlay.ass"
const outputFile = "demo.mp4"

const defaultFps = 30
const minFps = 10
const maxFps = 60

// Encoder-specific rendering defaults (CRF, preset, pixel format) are not part of the
// deterministic project plan; they are internal to render implementation.
const defaultCRF = 19
const defaultPreset = "medium"
const defaultPixelFormat = "yuv420p"

// ChooseFps selects a frame rate from a video probe, falling back to a sane default if needed.
func ChooseFps(probe VideoProbe) int {
	var candidate *float64
	if probe.AvgFrameRate != nil {
		candidate = probe.AvgFrameRate
	} else {
		candidate = probe.RealFrameRate
	}
	if candidate == nil {
		return defaultFps
	}
	rounded := int(jsMathRound(*candidate))
	if rounded < minFps || rounded > maxFps {
		return defaultFps
	}
	return rounded
}

// jsMathRound matches JS's Math.round exactly: floor(x+0.5) for every x, including
// negatives, where it rounds half toward +Infinity rather than away from zero the way
// math.Round does (docs/plans/go-port.md, "traps to write tests for first" #2). Frame
// rates are never negative in practice, but this is the general form so the mismatch can't
// resurface if this helper is ever reused somewhere that isn't.
func jsMathRound(v float64) float64 {
	return math.Floor(v + 0.5)
}

func hasOverlay(project director.Project) bool {
	return (project.Cursor.Start != nil) || len(project.Cursor.Clicks) > 0 || len(project.Annotations) > 0
}

// Result is the output of rendering a project.
type Result struct {
	Output     string
	Width      int
	Height     int
	DurationMs *int64
}

// Project compiles a director project into a video file.
// Project contains all data needed for rendering; config is no longer needed.
func Project(dir string, project director.Project, logf func(format string, args ...any)) (Result, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(filepath.Join(dir, tmpDir), 0o750); err != nil { // #nosec G301
		return Result{}, fmt.Errorf("create temp directory: %w", err)
	}

	rawProbe, err := ProbeVideo(filepath.Join(dir, project.Source))
	if err != nil {
		return Result{}, err
	}
	fps := ChooseFps(rawProbe)
	logf("source %dx%d -> %d fps", rawProbe.Width, rawProbe.Height, fps)

	logf("pass A: %d segment(s), %d ms output", len(project.Timeline), project.DurationMs)
	if renderErr := Temporal(dir, project.Source, project.Timeline, cleanFile, fps); renderErr != nil {
		return Result{}, fmt.Errorf("render temporal: %w", renderErr)
	}

	video, err := ProbeVideo(filepath.Join(dir, cleanFile))
	if err != nil {
		return Result{}, err
	}
	wantMs := project.DurationMs
	gotMs := int64(0)
	if video.DurationMs != nil {
		gotMs = *video.DurationMs
	}
	// Each segment is encoded to CFR independently, so each can legitimately round to the
	// nearest frame boundary on its own (Pass A's `-t` bounds a segment's own clip, not the
	// sub-frame instant its planned duration falls on) — tolerance has to scale with segment
	// count rather than staying a flat single frame, or a real recording with a few dozen
	// segments trips this guard on harmless rounding instead of an actual dropped-frame bug,
	// which is still off by whole seconds, not a handful of frames.
	frameMs := int64(1000 / fps)
	tolerance := frameMs * int64(len(project.Timeline)+1)
	if diff := gotMs - wantMs; diff > tolerance || diff < -tolerance {
		return Result{}, fmt.Errorf(
			"pass A produced %dms of video but project.json expects %dms (off by %dms) — refusing to continue into pass B with mismatched audio/video length",
			gotMs, wantMs, diff,
		)
	}

	assFile := ""
	if hasOverlay(project) {
		ass := BuildAss(project, video, fps)
		if assErr := os.WriteFile(filepath.Join(dir, assFileName), []byte(ass), 0o600); assErr != nil { // #nosec G306
			return Result{}, fmt.Errorf("write ASS file: %w", assErr)
		}
		assFile = assFileName
	}

	graph := BuildVisualFilterGraph(project.Camera, project.Viewport, video, fps, assFile)
	audioGraph := BuildAudioFilterGraph(project.Audio, project.Voice, project.DurationMs)
	graph = joinFilterGraphs(graph, audioGraph)
	assets, err := writeAudioAssets(dir)
	if err != nil {
		return Result{}, err
	}
	voiceClipPaths := make([]string, len(project.Voice))
	for i, vc := range project.Voice {
		voiceClipPaths[i] = vc.AudioFile
	}

	logf("pass B: %d camera keyframe(s), %d annotation(s), %d audio cue(s), %d voice cue(s)",
		len(project.Camera), len(project.Annotations), len(project.Audio), len(project.Voice))
	if graphErr := os.WriteFile(filepath.Join(dir, passBFilter), []byte(graph), 0o600); graphErr != nil { // #nosec G306
		return Result{}, fmt.Errorf("write pass B filter: %w", graphErr)
	}
	// Render-specific parameters (CRF, preset, format) are encoder choice, not deterministic visual plan.
	if renderErr := Visual(dir, cleanFile, passBFilter, outputFile, fps, defaultCRF, defaultPreset, defaultPixelFormat, assets, voiceClipPaths); renderErr != nil {
		return Result{}, renderErr
	}

	// Intermediates only survive a failure, where they are useful for diagnosis (spec 10).
	if removeErr := os.RemoveAll(filepath.Join(dir, tmpDir)); removeErr != nil {
		return Result{}, fmt.Errorf("remove intermediates: %w", removeErr)
	}

	result, err := ProbeVideo(filepath.Join(dir, outputFile))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Output:     filepath.Join(dir, outputFile),
		Width:      result.Width,
		Height:     result.Height,
		DurationMs: result.DurationMs,
	}, nil
}

func joinFilterGraphs(videoGraph, audioGraph string) string {
	if videoGraph == "" {
		videoGraph = "[0:v]null[out]"
	}
	return videoGraph + ";\n" + audioGraph
}
