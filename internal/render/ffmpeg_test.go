package render

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

func TestResolveBinPrefersEnvOverride(t *testing.T) {
	t.Setenv("DEMO_RECORDER_TEST_BIN", "/explicit/path/to/tool")
	if got := resolveBin("DEMO_RECORDER_TEST_BIN", "tool"); got != "/explicit/path/to/tool" {
		t.Errorf("resolveBin = %q, want the env override", got)
	}
}

func TestResolveBinFindsItOnPath(t *testing.T) {
	dir := t.TempDir()
	name := "take5-test-tool"
	path := writeFakeExecutable(t, dir, name)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := resolveBin("DEMO_RECORDER_TEST_BIN_UNSET", name); got != path {
		t.Errorf("resolveBin = %q, want %q", got, path)
	}
}

// The regression this guards: a Chrome-spawned process's PATH is the OS default
// (/usr/bin:/bin:/usr/sbin:/sbin, SPIKE.md §5), which does not include Homebrew's
// /opt/homebrew/bin — so a real ffmpeg install can still be invisible via plain PATH lookup.
func TestResolveBinFallsBackToKnownInstallDirs(t *testing.T) {
	dir := t.TempDir()
	name := "take5-test-tool"
	path := writeFakeExecutable(t, dir, name)

	original := extraBinDirs
	extraBinDirs = []string{dir}
	t.Cleanup(func() { extraBinDirs = original })
	t.Setenv("PATH", "") // simulate the tool being absent from this process's own PATH

	if got := resolveBin("DEMO_RECORDER_TEST_BIN_UNSET", name); got != path {
		t.Errorf("resolveBin = %q, want %q", got, path)
	}
}

func TestResolveBinFallsThroughToBareNameWhenNotFoundAnywhere(t *testing.T) {
	name := "take5-tool-that-does-not-exist"
	original := extraBinDirs
	extraBinDirs = []string{t.TempDir()}
	t.Cleanup(func() { extraBinDirs = original })
	t.Setenv("PATH", "")

	if got := resolveBin("DEMO_RECORDER_TEST_BIN_UNSET", name); got != name {
		t.Errorf("resolveBin = %q, want the bare name %q", got, name)
	}
}
