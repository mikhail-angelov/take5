package voiceover

import "testing"

func cues3(t *testing.T) []Cue {
	t.Helper()
	return []Cue{
		{ID: "cue-0", SourceStartMs: 0, SourceEndMs: 2000, OriginalText: "so first I'll uh click generate"},
		{ID: "cue-1", SourceStartMs: 2000, SourceEndMs: 5000, OriginalText: "and now watch what happens"},
		{ID: "cue-2", SourceStartMs: 5000, SourceEndMs: 5800, OriginalText: "um"},
	}
}

func TestValidateRewriteResponseAcceptsFullCoverage(t *testing.T) {
	cues := cues3(t)
	response := map[string]RewriteOutcome{
		"cue-0": {Text: "First, I'll click Generate."},
		"cue-1": {Text: "Now watch what happens."},
		"cue-2": {Dropped: true},
	}
	if err := ValidateRewriteResponse(cues, response); err != nil {
		t.Errorf("ValidateRewriteResponse = %v, want nil", err)
	}
}

// The exact adversarial case the MVP hit: a 6-cue batch came back as 5, with the model
// silently dropping a trailing cue by omission rather than marking it dropped. Positional
// matching wouldn't catch this; ID-based coverage must.
func TestValidateRewriteResponseFailsWhenAnIDIsSilentlyMissing(t *testing.T) {
	cues := cues3(t)
	response := map[string]RewriteOutcome{
		"cue-0": {Text: "First, I'll click Generate."},
		"cue-1": {Text: "Now watch what happens."},
		// cue-2 missing entirely, not even marked dropped.
	}
	err := ValidateRewriteResponse(cues, response)
	if err == nil {
		t.Fatal("expected an error when a cue id is missing from the response")
	}
}

func TestValidateRewriteResponseFailsWhenAMiddleIDIsMissing(t *testing.T) {
	cues := cues3(t)
	response := map[string]RewriteOutcome{
		"cue-0": {Text: "First, I'll click Generate."},
		"cue-2": {Dropped: true},
		// cue-1 missing — a dropped element in the middle, the case positional matching
		// would silently misalign every cue after it.
	}
	if err := ValidateRewriteResponse(cues, response); err == nil {
		t.Fatal("expected an error when a middle cue id is missing from the response")
	}
}

func TestParseRewriteResponseHandlesTextAndDropped(t *testing.T) {
	raw := []byte(`{
		"cue-0": {"text": "First, I'll click Generate."},
		"cue-1": {"text": "Now watch what happens."},
		"cue-2": {"dropped": true}
	}`)
	got, err := parseRewriteResponse(raw)
	if err != nil {
		t.Fatalf("parseRewriteResponse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got["cue-0"].Dropped || got["cue-0"].Text != "First, I'll click Generate." {
		t.Errorf("cue-0 = %+v", got["cue-0"])
	}
	if !got["cue-2"].Dropped {
		t.Errorf("cue-2.Dropped = false, want true")
	}
}

func TestParseRewriteResponseTreatsMissingTextAsDropped(t *testing.T) {
	// A malformed entry (neither text nor an explicit dropped flag) is treated as dropped
	// rather than silently synthesizing empty narration.
	got, err := parseRewriteResponse([]byte(`{"cue-0": {}}`))
	if err != nil {
		t.Fatalf("parseRewriteResponse: %v", err)
	}
	if !got["cue-0"].Dropped {
		t.Errorf("cue-0.Dropped = false, want true for an entry with neither text nor dropped")
	}
}

func TestParseRewriteResponseRejectsInvalidJSON(t *testing.T) {
	if _, err := parseRewriteResponse([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}
