package render

import (
	"fmt"
	"strconv"
	"strings"

	"take5/internal/director"
)

// Pass B — visual edit (spec 22.2): camera zoom/pan, then the burned-in overlay carrying
// the synthetic cursor, click pulse and annotations. Direct port of
// src/render/visual-renderer.js.

// BuildCameraFilter generates the FFmpeg filter for camera effects.
func BuildCameraFilter(camera []director.CameraKeyframe, viewport director.Viewport, video VideoProbe, fps int) string {
	if len(camera) == 0 {
		return ""
	}

	sx := float64(video.Width) / viewport.Width
	sy := float64(video.Height) / viewport.Height

	zoomKfs := make([]Keyframe, len(camera))
	centreXKfs := make([]Keyframe, len(camera))
	centreYKfs := make([]Keyframe, len(camera))
	for i, k := range camera {
		zoomKfs[i] = Keyframe{TMs: k.TMs, Value: k.Zoom}
		centreXKfs[i] = Keyframe{TMs: k.TMs, Value: k.Cx * sx}
		centreYKfs[i] = Keyframe{TMs: k.TMs, Value: k.Cy * sy}
	}

	zoom := PiecewiseExpression(zoomKfs, "ot")
	centreX := PiecewiseExpression(centreXKfs, "ot")
	centreY := PiecewiseExpression(centreYKfs, "ot")

	return fmt.Sprintf(
		"zoompan=z='%s':x='%s':y='%s':d=1:s=%dx%d:fps=%d",
		zoom, CropOriginExpression(centreX, "x"), CropOriginExpression(centreY, "y"),
		video.Width, video.Height, fps,
	)
}

// BuildVisualFilterGraph generates the complete FFmpeg filter graph for the visual pass.
func BuildVisualFilterGraph(camera []director.CameraKeyframe, viewport director.Viewport, video VideoProbe, fps int, assFile string) string {
	var chain []string
	if cameraFilter := BuildCameraFilter(camera, viewport, video, fps); cameraFilter != "" {
		chain = append(chain, cameraFilter)
	}
	if assFile != "" {
		chain = append(chain, fmt.Sprintf("ass=%s", assFile))
	}
	if len(chain) == 0 {
		return ""
	}
	return fmt.Sprintf("[0:v]%s[out]", strings.Join(chain, ","))
}

// VisualArgs builds the ffmpeg Pass B argument list. voiceClipPaths are per-session files,
// not embedded assets like assets.Music/Click/Typing (docs/plans/2026-08-19-voice-annotations.md
// Task 6), so they arrive as paths rather than through audioAssets. Appended after typing, in
// project.Voice order — BuildAudioFilterGraph's voiceInputStart (4) assumes exactly this
// ordering.
func VisualArgs(source, filterScriptPath, output string, fps, crf int, preset, pixelFormat string, assets audioAssets, voiceClipPaths []string) []string {
	args := []string{
		"-y",
		"-i", source,
		"-stream_loop", "-1", "-i", assets.Music,
		"-i", assets.Click,
		"-i", assets.Typing,
	}
	for _, path := range voiceClipPaths {
		args = append(args, "-i", path)
	}
	return append(args,
		"-filter_complex_script", filterScriptPath,
		"-map", "[out]",
		"-map", "[audio]",
		"-r", strconv.Itoa(fps),
		"-fps_mode", "cfr",
		"-c:v", "libx264",
		"-preset", preset,
		"-crf", strconv.Itoa(crf),
		"-c:a", "aac",
		"-b:a", "128k",
		"-pix_fmt", pixelFormat,
		"-movflags", "+faststart",
		output,
	)
}

// Visual runs ffmpeg Pass B: camera zoom/pan, the burned-in overlay, and the final
// audio mix (music/click/typing/voice) muxed onto cleanFile's video.
func Visual(cwd, source, filterScriptPath, output string, fps, crf int, preset, pixelFormat string, assets audioAssets, voiceClipPaths []string) error {
	return RunFfmpeg(VisualArgs(source, filterScriptPath, output, fps, crf, preset, pixelFormat, assets, voiceClipPaths), cwd)
}
