package install

import (
	"os"
	"path/filepath"
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

// Doctor's whisper.cpp fields are tested through WHISPER_CLI_PATH/WHISPER_MODEL_PATH, the one
// override transcribe.WhisperBin/ModelPath always honor before touching PATH or any
// machine-specific install directory — the only way to make "absent" deterministic across dev
// machines that may or may not have whisper-cpp installed via Homebrew.

func TestDoctorReportsWhisperFoundViaExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeExecutable(t, dir, "whisper-cli")
	t.Setenv("WHISPER_CLI_PATH", path)

	report := Doctor("")
	if !report.WhisperFound || report.WhisperPath != path {
		t.Errorf("WhisperFound=%v WhisperPath=%q, want found at %q", report.WhisperFound, report.WhisperPath, path)
	}
}

func TestDoctorReportsWhisperAbsentWhenExplicitPathDoesNotExist(t *testing.T) {
	t.Setenv("WHISPER_CLI_PATH", filepath.Join(t.TempDir(), "whisper-cli-that-does-not-exist"))

	report := Doctor("")
	if report.WhisperFound {
		t.Errorf("WhisperFound = true for a WHISPER_CLI_PATH that doesn't exist (WhisperPath=%q)", report.WhisperPath)
	}
}

func TestDoctorReportsWhisperModelFound(t *testing.T) {
	model := filepath.Join(t.TempDir(), "ggml-small.bin")
	if err := os.WriteFile(model, []byte("fake model"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHISPER_MODEL_PATH", model)

	report := Doctor("")
	if !report.WhisperModelFound || report.WhisperModelPath != model {
		t.Errorf("WhisperModelFound=%v WhisperModelPath=%q, want found at %q", report.WhisperModelFound, report.WhisperModelPath, model)
	}
}

func TestDoctorReportsWhisperModelMissing(t *testing.T) {
	t.Setenv("WHISPER_MODEL_PATH", filepath.Join(t.TempDir(), "ggml-small.bin"))

	report := Doctor("")
	if report.WhisperModelFound {
		t.Errorf("WhisperModelFound = true for a WHISPER_MODEL_PATH that doesn't exist (WhisperModelPath=%q)", report.WhisperModelPath)
	}
}

func TestDoctorReportsEdgeTTSFoundViaExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeExecutable(t, dir, "edge-tts")
	t.Setenv("EDGE_TTS_PATH", path)

	report := Doctor("")
	if !report.EdgeTTSFound || report.EdgeTTSPath != path {
		t.Errorf("EdgeTTSFound=%v EdgeTTSPath=%q, want found at %q", report.EdgeTTSFound, report.EdgeTTSPath, path)
	}
}

func TestDoctorReportsEdgeTTSAbsentWhenExplicitPathDoesNotExist(t *testing.T) {
	t.Setenv("EDGE_TTS_PATH", filepath.Join(t.TempDir(), "edge-tts-that-does-not-exist"))

	report := Doctor("")
	if report.EdgeTTSFound {
		t.Errorf("EdgeTTSFound = true for an EDGE_TTS_PATH that doesn't exist (EdgeTTSPath=%q)", report.EdgeTTSPath)
	}
}
