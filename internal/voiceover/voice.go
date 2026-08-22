package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"take5/internal/session"
	"take5/internal/transcribe"
)

// VoiceCue is one entry in voice.json — the plan's own schema (Technical Details section):
// a stable id, the ASR segment's source-time bounds and raw text, the LLM's rewrite (nil
// means an explicit drop), and — for surviving cues — the synthesized clip's file and
// duration. A dropped cue stays in this array with RewrittenText == nil rather than being
// omitted: internal/director's Task 5 is what skips it when building project.json, not this
// stage.
type VoiceCue struct {
	ID            string  `json:"id"`
	SourceStartMs int64   `json:"sourceStartMs"`
	SourceEndMs   int64   `json:"sourceEndMs"`
	OriginalText  string  `json:"originalText"`
	RewrittenText *string `json:"rewrittenText"`
	// Language is whisper.cpp's detected language for the whole transcript (e.g. "ru"), not
	// per-segment — repeated on every cue from one Run rather than hoisted to a document-level
	// field so voice.json's top level stays a plain array, matching every reader of it
	// (cmd/take5's analyzeSessionDir, internal/director.VoiceCue) without a schema
	// migration. Exists so both a human reading voice.json and DefaultVoiceForLanguage's
	// caller (Run, below) have visibility into what actually drove the TTS voice choice.
	Language      string `json:"language,omitempty"`
	AudioFile     string `json:"audioFile,omitempty"`
	TTSDurationMs int64  `json:"ttsDurationMs,omitempty"`
}

// Options configures one Run over one session directory.
type Options struct {
	Rewriter Rewriter
	TTS      TTSProvider
	// Voice is a TTS-provider-specific voice name (e.g. an edge-tts voice id).
	Voice string
	// ModelPath is a whisper.cpp ggml model file — see internal/transcribe.ModelPath.
	ModelPath string
	// Language is a whisper.cpp language code, or "" to auto-detect.
	Language string
	// ProbeDurationMs measures a written audio file's duration in milliseconds. Defaults to
	// an ffprobe-backed implementation (probeDurationMs below); overridable so tests can
	// avoid depending on a real audio codec round trip.
	ProbeDurationMs func(path string) (int64, error)
}

// transcodeToWav produces the 16kHz mono PCM WAV whisper.cpp needs from the raw MediaRecorder
// capture (session.VoiceFile, webm/opus) internal/session wrote — the transcode this stage
// owns per internal/session's own "no post-production decisions" invariant (see that
// package's doc comment and VoiceFile's).
func transcodeToWav(webmPath string) (wavPath string, cleanup func(), err error) {
	tmp, createErr := os.CreateTemp("", "take5-voice-*.wav")
	if createErr != nil {
		return "", func() {}, fmt.Errorf("voiceover: could not create a temp WAV file: %w", createErr)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	cleanup = func() { _ = os.Remove(tmpPath) }

	if runErr := runFfmpeg([]string{
		"-y", "-i", webmPath,
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
		tmpPath,
	}); runErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("voiceover: could not transcode %s to WAV: %w", webmPath, runErr)
	}
	return tmpPath, cleanup, nil
}

// swapVoiceDir replaces voiceDir with stagingDir, moving any existing voiceDir aside first and
// restoring it if the swap itself fails. It deliberately does not delete the aside-moved
// directory on success — voice.json's own rename still has to happen after this, and the
// caller needs the previous voice/ intact until that succeeds too, so both artifacts can be
// rolled back together on a late failure instead of leaving voice.json referencing clips that
// no longer match it.
func swapVoiceDir(voiceDir, stagingDir string) (oldVoiceDir string, err error) {
	if _, statErr := os.Stat(voiceDir); statErr == nil {
		oldVoiceDir = voiceDir + ".prev"
		if renameErr := os.Rename(voiceDir, oldVoiceDir); renameErr != nil {
			return "", fmt.Errorf("voiceover: could not move aside the previous %s: %w", voiceDir, renameErr)
		}
	}
	if renameErr := os.Rename(stagingDir, voiceDir); renameErr != nil {
		if oldVoiceDir != "" {
			_ = os.Rename(oldVoiceDir, voiceDir)
		}
		return "", fmt.Errorf("voiceover: could not move staged clips into %s: %w", voiceDir, renameErr)
	}
	return oldVoiceDir, nil
}

func cuesFromSegments(segments []transcribe.Segment) []Cue {
	cues := make([]Cue, len(segments))
	for i, seg := range segments {
		cues[i] = Cue{
			ID:            fmt.Sprintf("cue-%d", i),
			SourceStartMs: seg.StartMs,
			SourceEndMs:   seg.EndMs,
			OriginalText:  seg.Text,
		}
	}
	return cues
}

// synthesizeCues runs the rewrite + TTS half of the pipeline against already-transcribed
// cues, and writes voice.json + voice/*.mp3 into sessionDir. Split out from Run so it can be
// tested with fake Rewriter/TTSProvider implementations, without needing a real whisper.cpp
// transcription first. language is whisper.cpp's detected language for the whole transcript
// (Run passes transcript.Language; callers with no real transcription, i.e. tests, pass "");
// it's stamped onto every resulting VoiceCue but does not affect opts.Voice here — Run is
// where that selection already happened, using the same value.
func synthesizeCues(ctx context.Context, sessionDir string, cues []Cue, language string, opts Options) ([]VoiceCue, error) {
	probeDuration := opts.ProbeDurationMs
	if probeDuration == nil {
		probeDuration = probeDurationMs
	}

	response, rewriteErr := opts.Rewriter.Rewrite(ctx, cues)
	if rewriteErr != nil {
		return nil, fmt.Errorf("voiceover: rewrite failed: %w", rewriteErr)
	}
	if validateErr := ValidateRewriteResponse(cues, response); validateErr != nil {
		return nil, validateErr
	}

	// Cues are synthesized into a staging directory, not voiceDir itself, so a failure
	// partway through (or a probe failure on the last cue) leaves any previous voice/ and
	// voice.json from an earlier successful run untouched — see stagingDir's swap into
	// voiceDir below.
	voiceDir := filepath.Join(sessionDir, "voice")
	stagingDir := ""
	if len(cues) > 0 {
		var mkdirErr error
		stagingDir, mkdirErr = os.MkdirTemp(sessionDir, "voice-staging-*")
		if mkdirErr != nil {
			return nil, fmt.Errorf("voiceover: could not create a staging directory for %s: %w", voiceDir, mkdirErr)
		}
		defer os.RemoveAll(stagingDir) // no-op once stagingDir is renamed into place below
	}

	result := make([]VoiceCue, 0, len(cues))
	for _, cue := range cues {
		outcome := response[cue.ID]
		vc := VoiceCue{
			ID: cue.ID, SourceStartMs: cue.SourceStartMs, SourceEndMs: cue.SourceEndMs,
			OriginalText: cue.OriginalText, Language: language,
		}
		if outcome.Dropped {
			result = append(result, vc)
			continue
		}
		text := outcome.Text
		vc.RewrittenText = &text

		audio, synthErr := opts.TTS.Synthesize(ctx, text, opts.Voice)
		if synthErr != nil {
			return nil, fmt.Errorf("voiceover: TTS failed for %s: %w", cue.ID, synthErr)
		}
		relPath := filepath.Join("voice", cue.ID+".mp3")
		stagedPath := filepath.Join(stagingDir, cue.ID+".mp3")
		if writeErr := os.WriteFile(stagedPath, audio, 0o600); writeErr != nil {
			return nil, fmt.Errorf("voiceover: could not write %s: %w", stagedPath, writeErr)
		}
		durationMs, probeErr := probeDuration(stagedPath)
		if probeErr != nil {
			return nil, fmt.Errorf("voiceover: could not measure %s's duration: %w", stagedPath, probeErr)
		}
		vc.AudioFile = relPath
		vc.TTSDurationMs = durationMs
		result = append(result, vc)
	}

	buf, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("voiceover: could not encode voice.json: %w", err)
	}

	// voice.json is written to a temp path before voice/ is touched at all, so a failure here
	// leaves the previous run's voice/ and voice.json exactly as they were.
	voiceJSONPath := filepath.Join(sessionDir, "voice.json")
	tmpJSONPath := voiceJSONPath + ".tmp"
	if writeErr := os.WriteFile(tmpJSONPath, append(buf, '\n'), 0o600); writeErr != nil {
		return nil, fmt.Errorf("voiceover: could not write voice.json: %w", writeErr)
	}

	// Every cue synthesized and probed successfully: swap the staged clips in for the old
	// voice/, but keep that previous voice/ around under oldVoiceDir rather than deleting it
	// yet — voice.json's rename below is what actually commits this run, and until it
	// succeeds too, a failure needs to restore both voice/ and voice.json together rather
	// than leaving one updated and the other stale.
	oldVoiceDir := ""
	if stagingDir != "" {
		oldVoiceDir, err = swapVoiceDir(voiceDir, stagingDir)
		if err != nil {
			_ = os.Remove(tmpJSONPath)
			return nil, err
		}
	}

	if renameErr := os.Rename(tmpJSONPath, voiceJSONPath); renameErr != nil {
		if oldVoiceDir != "" {
			_ = os.RemoveAll(voiceDir)
			_ = os.Rename(oldVoiceDir, voiceDir)
		}
		return nil, fmt.Errorf("voiceover: could not finalize voice.json: %w", renameErr)
	}
	if oldVoiceDir != "" {
		_ = os.RemoveAll(oldVoiceDir)
	}
	return result, nil
}

// Run executes the whole `voice` stage for one session directory: session.VoiceFile
// (voice.webm) -> WAV -> whisper.cpp transcript -> LLM rewrite -> TTS -> voice.json +
// voice/*.mp3. Safe to call when there is no voice capture at all only in the sense that
// callers should check for session.VoiceFile's existence first (see the `voice` CLI
// subcommand) — Run itself assumes there is something to transcribe.
func Run(ctx context.Context, sessionDir string, opts Options) ([]VoiceCue, error) {
	wavPath, cleanup, err := transcodeToWav(filepath.Join(sessionDir, session.VoiceFile))
	if err != nil {
		return nil, err
	}
	defer cleanup()

	transcript, err := transcribe.Transcribe(wavPath, transcribe.Options{ModelPath: opts.ModelPath, Language: opts.Language})
	if err != nil {
		return nil, fmt.Errorf("voiceover: transcription failed: %w", err)
	}

	// opts.Voice left unset means "pick one from what whisper.cpp actually detected" —
	// opts.Language (a whisper.cpp *input* directive, "" meaning auto-detect) can't serve
	// that purpose itself, since it's frequently "" precisely when the caller doesn't know
	// the language ahead of time either. An explicit opts.Voice always wins.
	if opts.Voice == "" {
		opts.Voice = DefaultVoiceForLanguage(transcript.Language)
	}

	return synthesizeCues(ctx, sessionDir, cuesFromSegments(transcript.Segments), transcript.Language, opts)
}
