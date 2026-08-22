package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeRewriter and fakeTTS are the "fake/stub provider" this task's own testing note asks
// for — no real network call, no real edge-tts invocation.
type fakeRewriter struct {
	outcomes map[string]RewriteOutcome
	err      error
}

func (f *fakeRewriter) Rewrite(_ context.Context, _ []Cue) (map[string]RewriteOutcome, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.outcomes, nil
}

type fakeTTS struct {
	calls     int
	lastVoice string
}

func (f *fakeTTS) Synthesize(_ context.Context, text, voiceName string) ([]byte, error) {
	f.calls++
	f.lastVoice = voiceName
	return fmt.Appendf(nil, "fake-audio:%s:%s", voiceName, text), nil
}

func fakeProbe(durationsMs map[string]int64) func(string) (int64, error) {
	return func(path string) (int64, error) {
		if ms, ok := durationsMs[filepath.Base(path)]; ok {
			return ms, nil
		}
		return 1234, nil
	}
}

func TestSynthesizeCuesWritesVoiceJSONAndMP3sForKeptCues(t *testing.T) {
	dir := t.TempDir()
	cues := cues3(t)
	rewriter := &fakeRewriter{outcomes: map[string]RewriteOutcome{
		"cue-0": {Text: "First, I'll click Generate."},
		"cue-1": {Text: "Now watch what happens."},
		"cue-2": {Dropped: true},
	}}
	tts := &fakeTTS{}
	opts := Options{
		Rewriter:        rewriter,
		TTS:             tts,
		Voice:           "en-US-AriaNeural",
		ProbeDurationMs: fakeProbe(map[string]int64{"cue-0.mp3": 1800, "cue-1.mp3": 2900}),
	}

	result, err := synthesizeCues(context.Background(), dir, cues, "en", opts)
	if err != nil {
		t.Fatalf("synthesizeCues: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("len(result) = %d, want 3", len(result))
	}
	if tts.calls != 2 {
		t.Errorf("tts.calls = %d, want 2 (dropped cues must not be synthesized)", tts.calls)
	}

	kept := map[string]VoiceCue{}
	for _, vc := range result {
		kept[vc.ID] = vc
	}

	if kept["cue-0"].RewrittenText == nil || *kept["cue-0"].RewrittenText != "First, I'll click Generate." {
		t.Errorf("cue-0.RewrittenText = %v", kept["cue-0"].RewrittenText)
	}
	if kept["cue-0"].AudioFile != filepath.Join("voice", "cue-0.mp3") {
		t.Errorf("cue-0.AudioFile = %q", kept["cue-0"].AudioFile)
	}
	if kept["cue-0"].TTSDurationMs != 1800 {
		t.Errorf("cue-0.TTSDurationMs = %d, want 1800", kept["cue-0"].TTSDurationMs)
	}
	// Stamped on every cue, dropped or not — a human reading voice.json (or a future
	// DefaultVoiceForLanguage caller) needs it regardless of which cues survived rewriting.
	if kept["cue-0"].Language != "en" || kept["cue-2"].Language != "en" {
		t.Errorf("Language = %q / %q, want %q on every cue", kept["cue-0"].Language, kept["cue-2"].Language, "en")
	}

	// The dropped cue stays in voice.json (rewrittenText: null) rather than being omitted —
	// internal/director (Task 5) is what skips it when building project.json, not this stage.
	if kept["cue-2"].RewrittenText != nil {
		t.Errorf("cue-2.RewrittenText = %v, want nil (dropped)", kept["cue-2"].RewrittenText)
	}
	if kept["cue-2"].AudioFile != "" {
		t.Errorf("cue-2.AudioFile = %q, want empty for a dropped cue", kept["cue-2"].AudioFile)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "voice", "cue-0.mp3")); statErr != nil {
		t.Errorf("voice/cue-0.mp3 was not written: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice", "cue-2.mp3")); !os.IsNotExist(statErr) {
		t.Errorf("voice/cue-2.mp3 exists for a dropped cue (err=%v)", statErr)
	}

	voiceJSONBuf, err := os.ReadFile(filepath.Join(dir, "voice.json"))
	if err != nil {
		t.Fatalf("voice.json was not written: %v", err)
	}
	var onDisk []VoiceCue
	if err := json.Unmarshal(voiceJSONBuf, &onDisk); err != nil {
		t.Fatalf("voice.json is not valid JSON: %v", err)
	}
	if len(onDisk) != 3 {
		t.Errorf("voice.json has %d entries, want 3 (dropped cues are kept, not omitted)", len(onDisk))
	}
}

func TestSynthesizeCuesFailsLoudlyWhenRewriteDropsAnIDSilently(t *testing.T) {
	dir := t.TempDir()
	cues := cues3(t)
	// The MVP's own adversarial case: 3 cues sent, only 2 come back — not even the 3rd
	// marked dropped.
	rewriter := &fakeRewriter{outcomes: map[string]RewriteOutcome{
		"cue-0": {Text: "First, I'll click Generate."},
		"cue-1": {Text: "Now watch what happens."},
	}}
	tts := &fakeTTS{}
	opts := Options{Rewriter: rewriter, TTS: tts, ProbeDurationMs: fakeProbe(nil)}

	_, err := synthesizeCues(context.Background(), dir, cues, "en", opts)
	if err == nil {
		t.Fatal("expected an error when the rewrite response silently drops a cue id")
	}
	if tts.calls != 0 {
		t.Errorf("tts.calls = %d, want 0 — no synthesis should happen once validation fails", tts.calls)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "voice.json")); !os.IsNotExist(statErr) {
		t.Errorf("voice.json was written despite a failed validation (err=%v)", statErr)
	}
}

func TestSynthesizeCuesPropagatesRewriterError(t *testing.T) {
	dir := t.TempDir()
	rewriter := &fakeRewriter{err: fmt.Errorf("network exploded")}
	_, err := synthesizeCues(context.Background(), dir, cues3(t), "en", Options{Rewriter: rewriter, TTS: &fakeTTS{}})
	if err == nil {
		t.Fatal("expected an error when the rewriter itself fails")
	}
}

func TestSynthesizeCuesWithNoCuesWritesAnEmptyVoiceJSON(t *testing.T) {
	dir := t.TempDir()
	result, err := synthesizeCues(context.Background(), dir, nil, "en", Options{
		Rewriter: &fakeRewriter{outcomes: map[string]RewriteOutcome{}},
		TTS:      &fakeTTS{},
	})
	if err != nil {
		t.Fatalf("synthesizeCues: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
	buf, err := os.ReadFile(filepath.Join(dir, "voice.json"))
	if err != nil {
		t.Fatalf("voice.json was not written: %v", err)
	}
	if string(buf) != "[]\n" {
		t.Errorf("voice.json = %q, want an empty array", buf)
	}
	if _, err := os.Stat(filepath.Join(dir, "voice")); !os.IsNotExist(err) {
		t.Errorf("voice/ dir was created with no cues (err=%v)", err)
	}
}
