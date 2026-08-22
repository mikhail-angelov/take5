package voiceover

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"take5/internal/config"
)

// TTSProvider synthesizes one cue's rewritten text into an audio clip. Kept as a small
// interface, per this task's own instruction, so the production provider choice (stay on
// free edge-tts, used in the spike, or move to a paid engine like ElevenLabs for better
// prosody — an explicit open question in the plan) doesn't ripple through voice.go.
type TTSProvider interface {
	// Synthesize returns encoded audio bytes (mp3) for text, in the given voice.
	Synthesize(ctx context.Context, text, voiceName string) ([]byte, error)
}

// Mirrors internal/transcribe's resolveBin: duplicated rather than shared, matching this
// codebase's existing precedent of one small resolveBin per exec-wrapping package.
var edgeTTSExtraBinDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

// EdgeTTSBin resolves the edge-tts executable the same way Synthesize itself does, so
// internal/install's doctor check reports the exact binary (or absence of one) this package
// would actually invoke. Checked last, before the bare-name fallback: config.Config's
// EdgeTTSPath — recorded by `take5 setup-voice` after a pipx install, which lands in
// ~/.local/bin, not one of edgeTTSExtraBinDirs' common package-manager prefixes, and (like
// OPENROUTER_API_KEY) not something PATH reliably carries into a process Chrome spawns
// (SPIKE.md §5's PATH problem, again).
func EdgeTTSBin() string {
	if p := os.Getenv("EDGE_TTS_PATH"); p != "" {
		return p
	}
	if p, err := exec.LookPath("edge-tts"); err == nil {
		return p
	}
	for _, dir := range edgeTTSExtraBinDirs {
		candidate := filepath.Join(dir, "edge-tts")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if cfg, err := config.Load(); err == nil && cfg.EdgeTTSPath != "" {
		return cfg.EdgeTTSPath
	}
	return "edge-tts"
}

type edgeTTSProvider struct{}

// NewEdgeTTSProvider returns a TTSProvider backed by the `edge-tts` CLI (a pip-installed
// wrapper around Microsoft Edge's free TTS voices) — the spike-validated default path. Shells
// out rather than reimplementing edge-tts's own protocol, matching this project's established
// exec-wrapper convention for FFmpeg and whisper.cpp.
func NewEdgeTTSProvider() TTSProvider {
	return &edgeTTSProvider{}
}

const defaultTTSVoice = "en-US-GuyNeural"

// edgeTTSMaxAttempts and edgeTTSRetryDelay bound the retry described in Synthesize below.
const (
	edgeTTSMaxAttempts = 3
	edgeTTSRetryDelay  = 2 * time.Second
)

// languageVoices maps a whisper.cpp-detected ISO 639-1 language code to a reasonable default
// edge-tts neural voice for that language — a male voice in each case, by request. Every name
// here was checked against a real `edge-tts --list-voices` run, not guessed. Exists because
// Synthesize used to always fall back to defaultTTSVoice regardless of what language the
// transcript actually was — narrating Russian (or any non-English) text with an English voice
// doesn't produce accented English speech, it produces the voice mispronouncing the text
// phoneme-by-phoneme as if it were English, which is unintelligible, not just
// non-native-sounding.
var languageVoices = map[string]string{
	"en": defaultTTSVoice,
	"ru": "ru-RU-DmitryNeural",
	"es": "es-ES-AlvaroNeural",
	"fr": "fr-FR-HenriNeural",
	"de": "de-DE-ConradNeural",
	"it": "it-IT-DiegoNeural",
	"pt": "pt-BR-AntonioNeural",
	"ja": "ja-JP-KeitaNeural",
	"ko": "ko-KR-InJoonNeural",
	"zh": "zh-CN-YunxiNeural",
	"uk": "uk-UA-OstapNeural",
	"pl": "pl-PL-MarekNeural",
	"tr": "tr-TR-AhmetNeural",
	"nl": "nl-NL-MaartenNeural",
	"ar": "ar-SA-HamedNeural",
	"hi": "hi-IN-MadhurNeural",
}

// DefaultVoiceForLanguage resolves a sensible edge-tts voice for a whisper.cpp language code
// (e.g. "ru", "auto", ""). Falls back to defaultTTSVoice for anything not in languageVoices —
// including "auto"/"" (whisper.cpp's own auto-detect sentinel and its zero value) — since
// narrating in the wrong voice is still better than refusing to narrate a session at all just
// because its detected language isn't in this table yet.
func DefaultVoiceForLanguage(lang string) string {
	if v, ok := languageVoices[lang]; ok {
		return v
	}
	return defaultTTSVoice
}

func (p *edgeTTSProvider) Synthesize(ctx context.Context, text, voiceName string) ([]byte, error) {
	if voiceName == "" {
		voiceName = defaultTTSVoice
	}

	// Text travels through a temp file, not a CLI arg: rewritten narration can contain quotes,
	// unicode, or newlines that would need fragile shell-safe escaping otherwise.
	textFile, createErr := os.CreateTemp("", "take5-tts-text-*.txt")
	if createErr != nil {
		return nil, fmt.Errorf("voiceover: could not create a temp file for TTS text: %w", createErr)
	}
	defer func() { _ = os.Remove(textFile.Name()) }()
	if _, writeErr := textFile.WriteString(text); writeErr != nil {
		_ = textFile.Close()
		return nil, fmt.Errorf("voiceover: could not write TTS text: %w", writeErr)
	}
	if closeErr := textFile.Close(); closeErr != nil {
		return nil, fmt.Errorf("voiceover: could not write TTS text: %w", closeErr)
	}

	outFile := textFile.Name() + ".mp3"
	defer func() { _ = os.Remove(outFile) }()

	// edge-tts's underlying websocket call to Microsoft's speech endpoint intermittently
	// fails with "NoAudioReceived" for a request that is well-formed and succeeds on an
	// immediate retry — confirmed by hand-replaying an identical failed invocation. A single
	// flaky cue shouldn't drop narration for the whole video, so retry a bounded number of
	// times before giving up.
	var lastErr error
	for attempt := 1; attempt <= edgeTTSMaxAttempts; attempt++ {
		// voiceName and the two temp paths above are all this function's own construction,
		// not attacker-controlled input.
		cmd := exec.CommandContext(ctx, EdgeTTSBin(), "--voice", voiceName, "--file", textFile.Name(), "--write-media", outFile) //nolint:gosec // G204
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if runErr := cmd.Run(); runErr != nil {
			lastErr = fmt.Errorf("voiceover: edge-tts failed: %w (stderr: %s)", runErr, stderr.String())
		} else if audio, readErr := os.ReadFile(outFile); readErr != nil { //nolint:gosec // G304: outFile is this call's own temp path
			lastErr = fmt.Errorf("voiceover: edge-tts did not produce %s: %w", outFile, readErr)
		} else {
			return audio, nil
		}

		if attempt < edgeTTSMaxAttempts {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("voiceover: %w", ctx.Err())
			case <-time.After(edgeTTSRetryDelay):
			}
		}
	}
	return nil, lastErr
}
