package plate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Its own small resolveBin/extraBinDirs pair, not a shared one: internal/render,
// internal/transcribe and internal/voiceover each already carry this exact ~15 lines rather
// than sharing it, per SPIKE.md §5 (a Chrome-spawned host gets the OS default PATH, not the
// shell PATH Homebrew/MacPorts put the binary on) — see CLAUDE.md's Conventions section.
var extraBinDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

func resolveBin(envVar, name string) string {
	if p := os.Getenv(envVar); p != "" {
		return p
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range extraBinDirs {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}

func ffmpegBin() string  { return resolveBin("FFMPEG_PATH", "ffmpeg") }
func ffprobeBin() string { return resolveBin("FFPROBE_PATH", "ffprobe") }

// runFfmpeg runs FFmpeg with the given arguments, discarding stdout and surfacing stderr on
// failure — FFmpeg's own diagnosis of a bad input is far more useful than anything invented
// here. Every caller in this package already passes absolute paths (plate.Open resolves dir
// with filepath.Abs), so unlike internal/render's RunFfmpeg this needs no cwd parameter.
func runFfmpeg(args []string) error {
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)
	cmd := exec.Command(ffmpegBin(), full...) // #nosec G204
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("ffmpeg exited with code %d: %s", exitErr.ExitCode(), errBuf.String())
		}
		return fmt.Errorf("ffmpeg could not be started: %w", err)
	}
	return nil
}

type probeStream struct {
	CodecType string `json:"codec_type"`
}

type probeOutput struct {
	Streams []probeStream `json:"streams"`
}

// probeVideo confirms path is a decodable file with a video stream. It is the only
// verification a segment or the assembled raw.mp4 gets — no per-segment probe, matching
// AssemblePlate's existing trust level of the FFmpeg exit code plus atomic rename.
func probeVideo(path string) error {
	cmd := exec.Command(ffprobeBin(), //nolint:gosec // path is a session-local file this package itself just wrote
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_type",
		"-of", "json",
		path,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("ffprobe exited with code %d: %s", exitErr.ExitCode(), errBuf.String())
		}
		return fmt.Errorf("ffprobe could not be started: %w", err)
	}
	var parsed probeOutput
	if err := json.Unmarshal(outBuf.Bytes(), &parsed); err != nil {
		return fmt.Errorf("could not parse ffprobe output: %w", err)
	}
	if len(parsed.Streams) == 0 {
		return fmt.Errorf("no video stream found in %s", path)
	}
	return nil
}
