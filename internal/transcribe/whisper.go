// Package transcribe wraps whisper.cpp's whisper-cli binary as an exec subprocess — the same
// pattern internal/render uses for FFmpeg (see ffmpeg.go), not a cgo binding, per the
// deliberate privacy boundary in docs/plans/2026-08-19-voice-annotations.md ("Design
// decisions locked in"): ASR runs locally, and only the resulting text ever crosses the
// network in a later stage.
//
// Transcribe takes a WAV path, not the raw voice.webm the session writer produces
// (internal/session.VoiceFile) — whisper.cpp needs PCM, and the webm/opus -> WAV transcode is
// a post-production decision that belongs to the `voice` stage's orchestration
// (internal/voiceover), not here.
package transcribe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Mirrors internal/render/ffmpeg.go's resolveBin: a host Chrome spawns gets the system
// default PATH, not the shell PATH a Homebrew/MacPorts install put whisper-cli on.
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

// WhisperBin resolves the whisper-cli binary to run, independent of this process's own PATH.
func WhisperBin() string {
	return resolveBin("WHISPER_CLI_PATH", "whisper-cli")
}

// ManagedModelDir is where `take5 setup-voice` downloads a model to when none is
// found anywhere else — exported so that command can target the exact path modelDirs already
// searches, instead of duplicating the home-dir logic.
func ManagedModelDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "take5", "models"), nil
}

// modelDirs lists where a whisper.cpp model is plausibly already sitting: the Homebrew
// whisper-cpp formula's share directory, a couple of locations the project's own docs/setup
// instructions can point users at, and finally ManagedModelDir — checked last since a
// system-installed model, if any, should win over one setup-voice downloaded itself. A var,
// not a function, so tests can swap it out (see whisper_test.go).
var modelDirs = defaultModelDirs()

func defaultModelDirs() []string {
	dirs := []string{
		"/opt/homebrew/share/whisper-cpp",
		"/usr/local/share/whisper-cpp",
		"/opt/local/share/whisper-cpp",
	}
	if managed, err := ManagedModelDir(); err == nil {
		dirs = append(dirs, managed)
	}
	return dirs
}

// ModelPath resolves the ggml model file whisper-cli needs. WHISPER_MODEL_PATH, when set,
// names the file directly; otherwise every dir in modelDirs is searched for any ggml-*.bin
// file, since the exact model size (small, base, ...) is an operator choice, not this
// package's to assume.
func ModelPath() (string, bool) {
	if p := os.Getenv("WHISPER_MODEL_PATH"); p != "" {
		// p is an operator-set env var naming a local model file, the same trust boundary as
		// FFMPEG_PATH/WHISPER_CLI_PATH — not attacker-controlled input.
		info, err := os.Stat(p) //nolint:gosec // G703: operator-controlled path, see above
		if err == nil && !info.IsDir() {
			return p, true
		}
		return p, false
	}
	for _, dir := range modelDirs {
		matches, err := filepath.Glob(filepath.Join(dir, "ggml-*.bin"))
		if err != nil || len(matches) == 0 {
			continue
		}
		return matches[0], true
	}
	return "", false
}

// Segment is one utterance whisper.cpp separated the transcript into — the granularity voice
// cues are paired against (docs/plans/2026-08-19-voice-annotations.md's voice.json schema is
// per-segment, not per-word).
type Segment struct {
	StartMs int64
	EndMs   int64
	Text    string
}

// Result is one transcript, whisper.cpp's segments plus the language it auto-detected.
type Result struct {
	Language string
	Segments []Segment
}

// Options configures one Transcribe call.
type Options struct {
	// ModelPath is required — callers get it from ModelPath() (or an explicit override) and
	// are expected to have already turned a missing model into a user-facing error via the
	// doctor check, not here.
	ModelPath string
	// Language is a whisper.cpp language code, or "" / "auto" to auto-detect (the spike's own
	// validated path — see the plan's MVP findings).
	Language string
}

type whisperOffsets struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type whisperTranscription struct {
	Offsets whisperOffsets `json:"offsets"`
	Text    string         `json:"text"`
}

type whisperResult struct {
	Language string `json:"language"`
}

// whisperOutput mirrors whisper-cli's --output-json shape: a "result" object carrying the
// detected language, and a "transcription" array of segments with millisecond offsets.
type whisperOutput struct {
	Result        whisperResult          `json:"result"`
	Transcription []whisperTranscription `json:"transcription"`
}

// buildArgs constructs whisper-cli's argument list. Pure and separated from Transcribe so
// argument construction can be tested without running a real binary.
func buildArgs(wavPath string, opts Options, outBase string) []string {
	lang := opts.Language
	if lang == "" {
		lang = "auto"
	}
	return []string{
		"-m", opts.ModelPath,
		"-f", wavPath,
		"-oj",
		"-of", outBase,
		"-nt", // don't print a plain-text transcript to stdout; only the JSON file is read
		"-l", lang,
	}
}

// parseWhisperJSON turns whisper-cli's --output-json bytes into a Result. Pure and separated
// from Transcribe so output parsing can be tested against a fixture transcript instead of a
// real model invocation, per this task's own testing note.
func parseWhisperJSON(buf []byte) (Result, error) {
	var parsed whisperOutput
	if err := json.Unmarshal(buf, &parsed); err != nil {
		return Result{}, fmt.Errorf("transcribe: could not parse whisper-cli output: %w", err)
	}
	result := Result{Language: parsed.Result.Language}
	for _, seg := range parsed.Transcription {
		result.Segments = append(result.Segments, Segment{
			StartMs: seg.Offsets.From,
			EndMs:   seg.Offsets.To,
			Text:    seg.Text,
		})
	}
	return result, nil
}

// Transcribe runs whisper-cli against wavPath and parses its --output-json output. Mirrors
// internal/render.run's error-wrapping: whisper-cli's own stderr is more useful than anything
// this wrapper could synthesize.
func Transcribe(wavPath string, opts Options) (Result, error) {
	if opts.ModelPath == "" {
		return Result{}, fmt.Errorf("transcribe: a model path is required")
	}

	outDir, mkdirErr := os.MkdirTemp("", "take5-whisper-*")
	if mkdirErr != nil {
		return Result{}, fmt.Errorf("transcribe: could not create a temp dir for whisper-cli output: %w", mkdirErr)
	}
	defer os.RemoveAll(outDir)
	outBase := filepath.Join(outDir, "transcript")

	// WhisperBin() and buildArgs() resolve from operator-set env vars/config, not attacker
	// input — the same trust boundary internal/render.RunFfmpeg already shells out under.
	cmd := exec.Command(WhisperBin(), buildArgs(wavPath, opts, outBase)...) //nolint:gosec // G204
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		return Result{}, fmt.Errorf("transcribe: whisper-cli failed: %w (stderr: %s)", runErr, stderr.String())
	}

	// outBase is this call's own MkdirTemp path, not user input.
	buf, readErr := os.ReadFile(outBase + ".json") //nolint:gosec // G304
	if readErr != nil {
		return Result{}, fmt.Errorf("transcribe: whisper-cli did not produce %s.json: %w", outBase, readErr)
	}
	return parseWhisperJSON(buf)
}
