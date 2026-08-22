package voiceover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"take5/internal/session"
)

// Run is production's own entry point — session.VoiceFile in, voice.json + voice/*.mp3 out,
// through transcode -> transcribe -> language selection -> rewrite -> TTS -> atomic commit.
// Everything below fakes the external processes (ffmpeg, ffprobe, whisper-cli) the same way
// voice_test.go already fakes the network adapters (Rewriter, TTSProvider), so these tests
// exercise the real orchestration in Run without needing a real model, a real audio codec
// round trip, or network access. See improvement.md item 4.

func writeFakeExecutable(t *testing.T, dir, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable scripts are POSIX shell only")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // G306: a test fixture, not project output
		t.Fatal(err)
	}
	return path
}

const fakeFFmpegOKScript = "#!/bin/sh\nexit 0\n"
const fakeFFmpegFailScript = "#!/bin/sh\necho 'fake ffmpeg: boom' >&2\nexit 1\n"

const fakeFFprobeScript = `#!/bin/sh
cat <<'EOF'
{"format":{"duration":"1.5"}}
EOF
`

// fakeWhisperScript writes a canned whisper-cli --output-json transcript to "<outbase>.json",
// where outbase is whatever follows -of on the (fixed, per buildArgs) argument list — found by
// scanning rather than by position, so this fake stays correct even if buildArgs' order shifts.
const fakeWhisperOKScript = `#!/bin/sh
outbase=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-of" ]; then
    outbase="$arg"
  fi
  prev="$arg"
done
cat > "$outbase.json" <<'EOF'
{
  "result": { "language": "ru" },
  "transcription": [
    { "offsets": { "from": 0, "to": 1500 }, "text": " Нажмите Generate." },
    { "offsets": { "from": 1600, "to": 3000 }, "text": " Теперь смотрите результат." }
  ]
}
EOF
`

const fakeWhisperFailScript = "#!/bin/sh\necho 'fake whisper-cli: model not found' >&2\nexit 1\n"

func setupFakeFFmpeg(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FFMPEG_PATH", writeFakeExecutable(t, dir, "ffmpeg", script))
	t.Setenv("FFPROBE_PATH", writeFakeExecutable(t, dir, "ffprobe", fakeFFprobeScript))
}

func setupFakeWhisper(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("WHISPER_CLI_PATH", writeFakeExecutable(t, dir, "whisper-cli", script))
}

func fakeSessionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.VoiceFile), []byte("fake webm bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunProducesVoiceJSONThroughTheFullPipeline(t *testing.T) {
	setupFakeFFmpeg(t, fakeFFmpegOKScript)
	setupFakeWhisper(t, fakeWhisperOKScript)
	dir := fakeSessionDir(t)

	rewriter := &fakeRewriter{outcomes: map[string]RewriteOutcome{
		"cue-0": {Text: "Нажмите кнопку Generate."},
		"cue-1": {Text: "Смотрите на результат."},
	}}
	tts := &fakeTTS{}

	cues, err := Run(context.Background(), dir, Options{Rewriter: rewriter, TTS: tts, ModelPath: "/fake/model.bin"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("len(cues) = %d, want 2", len(cues))
	}
	if tts.calls != 2 {
		t.Errorf("tts.calls = %d, want 2", tts.calls)
	}
	for _, c := range cues {
		if c.Language != "ru" {
			t.Errorf("cue %s Language = %q, want ru (whisper-cli's detected language)", c.ID, c.Language)
		}
	}
	// opts.Voice was left unset, so Run must have picked the language-appropriate default
	// from whisper-cli's own detected language, not defaultTTSVoice.
	if tts.lastVoice != "ru-RU-DmitryNeural" {
		t.Errorf("TTS voice = %q, want the Russian default for a detected ru transcript", tts.lastVoice)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice.json")); statErr != nil {
		t.Errorf("voice.json was not written: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice", "cue-0.mp3")); statErr != nil {
		t.Errorf("voice/cue-0.mp3 was not written: %v", statErr)
	}
}

func TestRunFailsWhenTheWebmToWavTranscodeFails(t *testing.T) {
	setupFakeFFmpeg(t, fakeFFmpegFailScript)
	setupFakeWhisper(t, fakeWhisperOKScript)
	dir := fakeSessionDir(t)

	_, err := Run(context.Background(), dir, Options{Rewriter: &fakeRewriter{}, TTS: &fakeTTS{}, ModelPath: "/fake/model.bin"})
	if err == nil {
		t.Fatal("expected an error when the webm -> WAV transcode fails")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice.json")); !os.IsNotExist(statErr) {
		t.Errorf("voice.json was written despite a transcode failure (err=%v)", statErr)
	}
}

func TestRunFailsWhenTranscriptionFails(t *testing.T) {
	setupFakeFFmpeg(t, fakeFFmpegOKScript)
	setupFakeWhisper(t, fakeWhisperFailScript)
	dir := fakeSessionDir(t)

	_, err := Run(context.Background(), dir, Options{Rewriter: &fakeRewriter{}, TTS: &fakeTTS{}, ModelPath: "/fake/model.bin"})
	if err == nil {
		t.Fatal("expected an error when whisper-cli fails")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice.json")); !os.IsNotExist(statErr) {
		t.Errorf("voice.json was written despite a transcription failure (err=%v)", statErr)
	}
}

func TestRunLeavesNoCommittedVoiceJSONWhenRewriteFailsAfterTranscription(t *testing.T) {
	setupFakeFFmpeg(t, fakeFFmpegOKScript)
	setupFakeWhisper(t, fakeWhisperOKScript)
	dir := fakeSessionDir(t)

	rewriter := &fakeRewriter{err: fmt.Errorf("openrouter exploded")}
	tts := &fakeTTS{}

	_, err := Run(context.Background(), dir, Options{Rewriter: rewriter, TTS: tts, ModelPath: "/fake/model.bin"})
	if err == nil {
		t.Fatal("expected an error when the rewrite stage fails")
	}
	if tts.calls != 0 {
		t.Errorf("tts.calls = %d, want 0 — synthesis must not run once rewrite fails", tts.calls)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice.json")); !os.IsNotExist(statErr) {
		t.Errorf("voice.json was written despite a failed rewrite (err=%v)", statErr)
	}
}
