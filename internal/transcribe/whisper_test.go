package transcribe

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func writeFakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		path += ".bat"
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveBinPrefersEnvOverride(t *testing.T) {
	t.Setenv("DEMO_RECORDER_TEST_BIN", "/explicit/path/to/whisper-cli")
	if got := resolveBin("DEMO_RECORDER_TEST_BIN", "tool"); got != "/explicit/path/to/whisper-cli" {
		t.Errorf("resolveBin = %q, want the env override", got)
	}
}

func TestResolveBinFallsBackToKnownInstallDirs(t *testing.T) {
	dir := t.TempDir()
	name := "take5-test-whisper-cli"
	path := writeFakeExecutable(t, dir, name)

	original := extraBinDirs
	extraBinDirs = []string{dir}
	t.Cleanup(func() { extraBinDirs = original })
	t.Setenv("PATH", "")

	if got := resolveBin("DEMO_RECORDER_TEST_BIN_UNSET", name); got != path {
		t.Errorf("resolveBin = %q, want %q", got, path)
	}
}

func TestBuildArgsIncludesModelInputAndJSONOutput(t *testing.T) {
	args := buildArgs("/tmp/voice.wav", Options{ModelPath: "/models/ggml-small.bin", Language: "ru"}, "/tmp/out/transcript")
	want := []string{
		"-m", "/models/ggml-small.bin",
		"-f", "/tmp/voice.wav",
		"-oj",
		"-of", "/tmp/out/transcript",
		"-nt",
		"-l", "ru",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildArgs = %v, want %v", args, want)
	}
}

func TestBuildArgsDefaultsLanguageToAuto(t *testing.T) {
	args := buildArgs("/tmp/voice.wav", Options{ModelPath: "/models/ggml-small.bin"}, "/tmp/out/transcript")
	last := args[len(args)-1]
	if last != "auto" {
		t.Errorf("language arg = %q, want auto (the MVP's own validated auto-detect path)", last)
	}
}

// A fixture transcript, not a real model invocation — whisper-cli's actual --output-json
// shape, trimmed to what this wrapper reads.
const fixtureTranscript = `{
  "result": { "language": "ru" },
  "transcription": [
    { "offsets": { "from": 0, "to": 2500 }, "text": " Привет, это демо." },
    { "offsets": { "from": 2600, "to": 5100 }, "text": " Сейчас я покажу приложение." }
  ]
}`

func TestParseWhisperJSONExtractsSegments(t *testing.T) {
	result, err := parseWhisperJSON([]byte(fixtureTranscript))
	if err != nil {
		t.Fatalf("parseWhisperJSON: %v", err)
	}
	if result.Language != "ru" {
		t.Errorf("Language = %q, want ru", result.Language)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("len(Segments) = %d, want 2", len(result.Segments))
	}
	want := Segment{StartMs: 0, EndMs: 2500, Text: " Привет, это демо."}
	if result.Segments[0] != want {
		t.Errorf("Segments[0] = %+v, want %+v", result.Segments[0], want)
	}
	if result.Segments[1].StartMs != 2600 || result.Segments[1].EndMs != 5100 {
		t.Errorf("Segments[1] offsets = %d..%d, want 2600..5100", result.Segments[1].StartMs, result.Segments[1].EndMs)
	}
}

func TestParseWhisperJSONRejectsInvalidJSON(t *testing.T) {
	if _, err := parseWhisperJSON([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestParseWhisperJSONHandlesNoSpeechDetected(t *testing.T) {
	result, err := parseWhisperJSON([]byte(`{"result": {"language": "en"}, "transcription": []}`))
	if err != nil {
		t.Fatalf("parseWhisperJSON: %v", err)
	}
	if len(result.Segments) != 0 {
		t.Errorf("len(Segments) = %d, want 0", len(result.Segments))
	}
}

func TestTranscribeRequiresModelPath(t *testing.T) {
	if _, err := Transcribe("/tmp/voice.wav", Options{}); err == nil {
		t.Fatal("expected an error when ModelPath is empty")
	}
}

func TestModelPathReadsExplicitEnvOverride(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "ggml-small.bin")
	if err := os.WriteFile(model, []byte("fake model"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHISPER_MODEL_PATH", model)

	got, ok := ModelPath()
	if !ok || got != model {
		t.Errorf("ModelPath() = (%q, %v), want (%q, true)", got, ok, model)
	}
}

func TestModelPathReportsMissingExplicitFile(t *testing.T) {
	t.Setenv("WHISPER_MODEL_PATH", "/does/not/exist/ggml-small.bin")
	_, ok := ModelPath()
	if ok {
		t.Error("ModelPath() ok = true, want false for a WHISPER_MODEL_PATH that doesn't exist")
	}
}

func TestModelPathFindsAnyGgmlFileInKnownDirs(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "ggml-base.bin")
	if err := os.WriteFile(model, []byte("fake model"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHISPER_MODEL_PATH", "")

	original := modelDirs
	modelDirs = []string{dir}
	t.Cleanup(func() { modelDirs = original })

	got, ok := ModelPath()
	if !ok || got != model {
		t.Errorf("ModelPath() = (%q, %v), want (%q, true)", got, ok, model)
	}
}

func TestModelPathReportsNotFoundWhenNoKnownDirHasAModel(t *testing.T) {
	t.Setenv("WHISPER_MODEL_PATH", "")
	original := modelDirs
	modelDirs = []string{t.TempDir()}
	t.Cleanup(func() { modelDirs = original })

	if _, ok := ModelPath(); ok {
		t.Error("ModelPath() ok = true, want false when no model file exists anywhere")
	}
}
