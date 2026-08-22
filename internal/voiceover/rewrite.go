// Package voiceover is the `voice` pipeline stage: transcript -> LLM rewrite -> TTS ->
// voice.json + voice/*.mp3 (docs/plans/2026-08-19-voice-annotations.md Task 4). This is the
// only stage in the whole pipeline that touches the network or calls an external ASR
// process — analyze/render stay exactly as deterministic and offline as they are today.
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
)

// Cue is one ASR segment considered for a voice-over rewrite — the unit the LLM batch call is
// keyed on by stable ID. Binding requirement from the plan's MVP findings: positional
// (index-based) matching between input and output arrays is not acceptable, because the LLM
// cannot be trusted to preserve array length across a batch rewrite. Every cue therefore
// carries an ID that survives the round trip on its own.
type Cue struct {
	ID            string
	SourceStartMs int64
	SourceEndMs   int64
	OriginalText  string
}

// RewriteOutcome is what the LLM decided about one cue: either rewritten text, or an explicit
// drop (filler / incomplete). Dropped is never inferred from a missing ID — see
// ValidateRewriteResponse.
type RewriteOutcome struct {
	Text    string
	Dropped bool
}

// Rewriter turns raw ASR segments into cleaned narration text, one outcome per cue ID. Kept as
// an interface, mirroring the TTSProvider abstraction in tts.go, so the concrete LLM provider
// (OpenRouter + deepseek-chat in the pre-plan spike; not pinned by this plan — see "Open
// questions") can be swapped without changing the orchestration in voice.go.
type Rewriter interface {
	Rewrite(ctx context.Context, cues []Cue) (map[string]RewriteOutcome, error)
}

// ValidateRewriteResponse enforces the plan's binding requirement: every cue ID sent must come
// back, with either text or an explicit drop marker, never silently missing. Pulled out as a
// pure function specifically so the adversarial case the MVP actually hit — a 6-cue batch
// coming back with 5 — can be tested without a real network call.
func ValidateRewriteResponse(cues []Cue, response map[string]RewriteOutcome) error {
	var missing []string
	for _, c := range cues {
		if _, ok := response[c.ID]; !ok {
			missing = append(missing, c.ID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("voiceover: rewrite response is missing %d of %d cue ids: %v", len(missing), len(cues), missing)
	}
	return nil
}

type rewriteResponseEntry struct {
	Text    *string `json:"text,omitempty"`
	Dropped bool    `json:"dropped,omitempty"`
}

// parseRewriteResponse turns the LLM's raw JSON reply into outcomes keyed by cue ID. Expected
// shape: a JSON object mapping each cue id to either {"text": "..."} or {"dropped": true}.
// Pulled out as a pure function so it can be tested against a fixture response instead of a
// real model call, mirroring internal/transcribe's parseWhisperJSON.
func parseRewriteResponse(raw []byte) (map[string]RewriteOutcome, error) {
	var parsed map[string]rewriteResponseEntry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("voiceover: could not parse rewrite response: %w", err)
	}
	out := make(map[string]RewriteOutcome, len(parsed))
	for id, entry := range parsed {
		if entry.Dropped || entry.Text == nil {
			out[id] = RewriteOutcome{Dropped: true}
			continue
		}
		out[id] = RewriteOutcome{Text: *entry.Text}
	}
	return out, nil
}
