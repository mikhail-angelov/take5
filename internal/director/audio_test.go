package director

import "testing"

func TestPlanAudioMapsCuesToOutputTimeAndDebouncesTyping(t *testing.T) {
	timeline := []Segment{{
		SourceStartMs: 500,
		SourceEndMs:   1500,
		Speed:         2,
		OutputStartMs: 0,
		OutputEndMs:   500,
	}}
	actions := []Action{
		{Kind: "click", StartMs: 1000},
		{Kind: "input", StartMs: 1050},
		{Kind: "input", StartMs: 1070},
		{Kind: "input", StartMs: 1200},
		{Kind: "click", StartMs: 2000}, // Cut from the final video.
	}

	got := planAudio(actions, timeline, DefaultConfig())
	want := []AudioCue{
		{Kind: "click", TMs: 250},
		{Kind: "input", TMs: 275},
		{Kind: "input", TMs: 350},
	}
	if len(got) != len(want) {
		t.Fatalf("cue count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cue %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestPlanAudioNeedsNoCodeChangeForAFreeze locks in
// docs/plans/2026-08-21-voice-sync-and-freeze-frame.md Task 4's claim: planAudio only ever
// calls sourceTimeToOutputTime/isSourceTimeKept on action instants, so a held segment
// inserted between two actions shifts the SECOND action's cue by exactly HoldMs and leaves
// the first one untouched, with zero changes to audio.go itself.
func TestPlanAudioNeedsNoCodeChangeForAFreeze(t *testing.T) {
	actions := []Action{
		{Kind: "click", StartMs: 500},
		{Kind: "click", StartMs: 2000},
	}
	noHold := buildTimeline([]RawSegment{{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1}})
	withHold := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1},
		{SourceStartMs: 1000, SourceEndMs: 1000, HoldMs: 500},
	})

	before := planAudio(actions, noHold, DefaultConfig())
	after := planAudio(actions, withHold, DefaultConfig())
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("before = %#v, after = %#v, want 2 cues each", before, after)
	}
	if before[0].TMs != after[0].TMs {
		t.Errorf("cue before the freeze: TMs = %d, want unchanged at %d", after[0].TMs, before[0].TMs)
	}
	if got := after[1].TMs - before[1].TMs; got != 500 {
		t.Errorf("cue after the freeze shifted by %d, want exactly 500 (the HoldMs)", got)
	}
}
