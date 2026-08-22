// Package render compiles a project.json into demo.mp4 through the two FFmpeg passes
// (spec 22). Direct port of src/render/*.js — see docs/plans/go-port.md, Phase 3.
package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Thin wrapper around the system FFmpeg binaries. Everything above this file works with a
// data model; only this file knows about processes. Port of src/render/ffmpeg.js.

// SPIKE.md §5: a host Chrome spawns gets the system default PATH
// (/usr/bin:/bin:/usr/sbin:/sbin), not the shell PATH that put ffmpeg on the user's PATH in
// the first place — Homebrew on Apple Silicon and MacPorts both install outside it. The Node
// prototype baked an install-time-resolved absolute path into a generated launcher; the Go
// binary has no launcher to bake one into (install registers it directly), so instead this
// probes the well-known install prefixes directly at call time. Self-healing (no path to go
// stale across an ffmpeg upgrade) and covers the common case without needing an install-time
// step at all.
var extraBinDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

// FfmpegBin resolves the ffmpeg binary to run, independent of this process's own PATH.
func FfmpegBin() string {
	return resolveBin("FFMPEG_PATH", "ffmpeg")
}

// FfprobeBin resolves the ffprobe binary to run, independent of this process's own PATH.
func FfprobeBin() string {
	return resolveBin("FFPROBE_PATH", "ffprobe")
}

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
	// Falls through to the bare name so a genuinely missing ffmpeg still fails with a clear
	// "executable file not found in $PATH" rather than a confusing absolute-path error.
	return name
}

// FfmpegError is an error from FFmpeg execution.
type FfmpegError struct {
	Message string
	Args    []string
	Stderr  string
	Code    int
}

func (e *FfmpegError) Error() string { return e.Message }

// runWithOutput executes a command and captures stdout for processing.
func runWithOutput(bin string, args []string, cwd string) (stdout string, err error) {
	cmd := exec.Command(bin, args...) // #nosec G204
	if cwd != "" {
		cmd.Dir = cwd
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr := errBuf.String()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// FFmpeg's own diagnosis is far more useful than anything we could invent.
			return stdout, &FfmpegError{
				Message: fmt.Sprintf("%s exited with code %d", bin, exitErr.ExitCode()),
				Args:    args, Stderr: stderr, Code: exitErr.ExitCode(),
			}
		}
		return stdout, &FfmpegError{
			Message: fmt.Sprintf("%s could not be started: %s", bin, runErr), Args: args, Stderr: stderr, Code: -1,
		}
	}
	return stdout, nil
}

// run executes a command and returns only the error, discarding output.
func run(bin string, args []string, cwd string) error {
	_, err := runWithOutput(bin, args, cwd)
	return err
}

// IsFfmpegAvailable checks if FFmpeg is available on the system.
func IsFfmpegAvailable() bool {
	return run(FfmpegBin(), []string{"-hide_banner", "-version"}, "") == nil
}

// RunFfmpeg runs FFmpeg with the given arguments.
func RunFfmpeg(args []string, cwd string) error {
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)
	return run(FfmpegBin(), full, cwd)
}

func parseRate(rate string) (float64, bool) {
	parts := strings.SplitN(rate, "/", 2)
	if len(parts) != 2 {
		return 0, false
	}
	num, errN := strconv.ParseFloat(parts[0], 64)
	den, errD := strconv.ParseFloat(parts[1], 64)
	if errN != nil || errD != nil || den == 0 {
		return 0, false
	}
	return num / den, true
}

// VideoProbe contains the dimensions and frame rate of a video.
type VideoProbe struct {
	Width         int
	Height        int
	AvgFrameRate  *float64
	RealFrameRate *float64
	DurationMs    *int64
}

type probeStream struct {
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	RFrameRate   string `json:"r_frame_rate"`
}

type probeFormat struct {
	Duration string `json:"duration"`
}

type probeOutput struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

// ProbeVideo extracts dimensions and frame rate from a video file using FFprobe.
func ProbeVideo(path string) (VideoProbe, error) {
	stdout, err := runWithOutput(FfprobeBin(), []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,avg_frame_rate,r_frame_rate:format=duration",
		"-of", "json",
		path,
	}, "")
	if err != nil {
		return VideoProbe{}, err
	}

	var parsed probeOutput
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		return VideoProbe{}, fmt.Errorf("could not parse ffprobe output: %w", err)
	}

	var stream probeStream
	if len(parsed.Streams) > 0 {
		stream = parsed.Streams[0]
	}

	probe := VideoProbe{Width: stream.Width, Height: stream.Height}
	// MediaRecorder WebM is variable frame rate and often reports nonsense here, so the
	// caller decides what to trust.
	if avg, ok := parseRate(stream.AvgFrameRate); ok && avg > 0 && avg < 1000 {
		probe.AvgFrameRate = &avg
	}
	if rFrame, ok := parseRate(stream.RFrameRate); ok && rFrame > 0 && rFrame < 1000 {
		probe.RealFrameRate = &rFrame
	}
	if durationSec, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
		ms := int64(math.Round(durationSec * 1000))
		probe.DurationMs = &ms
	}
	return probe, nil
}
