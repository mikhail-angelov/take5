package voiceover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// Audio transcode (webm -> WAV for whisper.cpp) and duration probing (of synthesized TTS
// clips) are this stage's own concern, not internal/render's — see improvement.md item 4's
// "Дополнительное улучшение locality". Mirrors internal/render/ffmpeg.go's and
// internal/transcribe/whisper.go's resolveBin: duplicated rather than shared, matching this
// codebase's established one-resolveBin-per-exec-wrapping-package precedent — now its third
// occurrence, confirming it as convention rather than a one-off (see CLAUDE.md).
var mediaExtraBinDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

func resolveMediaBin(envVar, name string) string {
	if p := os.Getenv(envVar); p != "" {
		return p
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range mediaExtraBinDirs {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}

// ffmpegBin resolves the ffmpeg binary, independent of this process's own PATH. Shares
// FFMPEG_PATH with internal/render's FfmpegBin — same underlying executable, same operator
// override.
func ffmpegBin() string {
	return resolveMediaBin("FFMPEG_PATH", "ffmpeg")
}

// ffprobeBin resolves the ffprobe binary, independent of this process's own PATH. Shares
// FFPROBE_PATH with internal/render's FfprobeBin — same underlying executable, same operator
// override.
func ffprobeBin() string {
	return resolveMediaBin("FFPROBE_PATH", "ffprobe")
}

// runFfmpeg runs ffmpeg with the given arguments, discarding stdout — mirrors
// internal/render.RunFfmpeg's flags.
func runFfmpeg(args []string) error {
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)
	cmd := exec.Command(ffmpegBin(), full...) // #nosec G204 -- args are this package's own construction
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("voiceover: ffmpeg failed: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}

type probeFormat struct {
	Duration string `json:"duration"`
}

type probeOutput struct {
	Format probeFormat `json:"format"`
}

// probeDurationMs measures an audio (or video) file's duration in milliseconds via ffprobe —
// works on TTS-synthesized mp3 clips the same way internal/render's ProbeVideo works on the
// raw video capture.
func probeDurationMs(path string) (int64, error) {
	cmd := exec.Command(ffprobeBin(), // #nosec G204 -- path is this package's own construction
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("voiceover: could not probe %s: %w (stderr: %s)", path, err, stderr.String())
	}

	var parsed probeOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return 0, fmt.Errorf("voiceover: could not parse ffprobe output for %s: %w", path, err)
	}
	durationSec, err := strconv.ParseFloat(parsed.Format.Duration, 64)
	if err != nil {
		return 0, fmt.Errorf("voiceover: ffprobe reported no duration for %s", path)
	}
	return int64(math.Round(durationSec * 1000)), nil
}
