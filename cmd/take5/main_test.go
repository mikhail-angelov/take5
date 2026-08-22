package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// TestParsePositionalFlagsAcceptsDirectoryBeforeOrAfterFlags covers the documented
// `voice <session-dir> -force` invocation order (dir first) alongside the reverse: flag.Parse
// alone stops at the first non-flag token, which silently left -force unparsed when the
// directory came first.
func TestParsePositionalFlagsAcceptsDirectoryBeforeOrAfterFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"dir first", []string{"some-dir", "-force"}},
		{"dir last", []string{"-force", "some-dir"}},
		{"dir between value-taking flag and its value", []string{"-voice", "en-US-AriaNeural", "some-dir", "-force"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("voice", flag.ContinueOnError)
			force := fs.Bool("force", false, "")
			_ = fs.String("voice", "", "")

			dir := parsePositionalFlags(fs, tc.args)

			if dir != "some-dir" {
				t.Errorf("dir = %q, want %q", dir, "some-dir")
			}
			if !*force {
				t.Errorf("force = false, want true")
			}
		})
	}
}

// TestCmdVoiceIsANoOpWhenVoiceJSONAlreadyExists covers Task 8's re-run behavior: a re-run
// costs real LLM/TTS money, unlike analyze/render, so the default must be a no-op rather than
// render's always-regenerate-with--y behavior. No OpenRouter key or whisper model is set up
// here — if this test ever reached the actual pipeline it would os.Exit(1) and abort the test
// binary outright, so returning normally from this call is itself the assertion.
func TestCmdVoiceIsANoOpWhenVoiceJSONAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "voice.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdVoice([]string{dir})
}
