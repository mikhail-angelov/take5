package director

import "testing"

func strPtr(s string) *string { return &s }

func TestComputePairableUnitsCollapsesATypingRunIntoOneUnit(t *testing.T) {
	target := Target{Role: "textbox", Label: "Name"}
	actions := []Action{
		{Kind: "input", StartMs: 100, EndMs: 100, Event: Event{Kind: "input", Target: &target}},
		{Kind: "input", StartMs: 200, EndMs: 200, Event: Event{Kind: "input", Target: &target}},
		{Kind: "input", StartMs: 300, EndMs: 300, Event: Event{Kind: "input", Target: &target}},
		{Kind: "click", StartMs: 500, EndMs: 500},
	}
	units := computePairableUnits(actions)
	if len(units) != 2 {
		t.Fatalf("len(units) = %d, want 2 (one typing run + one click)", len(units))
	}
	if units[0].StartMs != 100 || units[0].EndMs != 300 || len(units[0].ActionIndexes) != 3 {
		t.Errorf("units[0] = %+v, want a run spanning 100..300 over 3 actions", units[0])
	}
	if units[1].StartMs != 500 || len(units[1].ActionIndexes) != 1 {
		t.Errorf("units[1] = %+v, want the lone click", units[1])
	}
}

func TestComputePairableUnitsSplitsRunsOnDifferentTargets(t *testing.T) {
	a := Target{Role: "textbox", Label: "A"}
	b := Target{Role: "textbox", Label: "B"}
	actions := []Action{
		{Kind: "input", StartMs: 100, EndMs: 100, Event: Event{Kind: "input", Target: &a}},
		{Kind: "input", StartMs: 200, EndMs: 200, Event: Event{Kind: "input", Target: &b}},
	}
	units := computePairableUnits(actions)
	if len(units) != 2 {
		t.Fatalf("len(units) = %d, want 2 — different fields must not merge into one run", len(units))
	}
}

func voiceCue(id string, startMs, endMs, ttsMs float64) VoiceCue {
	return VoiceCue{ID: id, SourceStartMs: startMs, SourceEndMs: endMs, RewrittenText: strPtr("x"), TTSDurationMs: ttsMs, AudioFile: "voice/" + id + ".mp3"}
}

func TestPairVoiceCuesPairsEachCueToTheNextActionAfterIt(t *testing.T) {
	units := []pairableUnit{
		{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}},
		{StartMs: 3000, EndMs: 3000, ActionIndexes: []int{1}},
	}
	cues := []VoiceCue{voiceCue("cue-0", 200, 900, 500), voiceCue("cue-1", 1500, 2800, 500)}
	pairings := pairVoiceCues(cues, units)
	if len(pairings) != 2 {
		t.Fatalf("len(pairings) = %d, want 2", len(pairings))
	}
	if pairings[0].Unit.StartMs != 1000 || pairings[1].Unit.StartMs != 3000 {
		t.Errorf("pairings = %+v, want cue-0->unit@1000, cue-1->unit@3000", pairings)
	}
}

func TestPairVoiceCuesDropsDroppedCuesBeforePairing(t *testing.T) {
	units := []pairableUnit{{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}}}
	cues := []VoiceCue{
		{ID: "cue-0", SourceStartMs: 200, SourceEndMs: 900, RewrittenText: nil}, // dropped
		voiceCue("cue-1", 200, 900, 500),
	}
	pairings := pairVoiceCues(keptVoiceCues(cues), units)
	if len(pairings) != 1 || pairings[0].Cue.ID != "cue-1" {
		t.Fatalf("pairings = %+v, want only cue-1", pairings)
	}
}

func TestPairVoiceCuesKeepsATrailingCueAsUnpairedRatherThanDroppingIt(t *testing.T) {
	units := []pairableUnit{{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}}}
	cues := []VoiceCue{voiceCue("cue-0", 200, 900, 500), voiceCue("cue-1", 5000, 5900, 500)}
	pairings := pairVoiceCues(cues, units)
	if len(pairings) != 2 {
		t.Fatalf("len(pairings) = %d, want 2 (cue-1 kept as a trailing cue, not dropped)", len(pairings))
	}
	if pairings[0].Unit == nil || pairings[0].Unit.StartMs != 1000 {
		t.Errorf("pairings[0] = %+v, want paired to the unit at 1000", pairings[0])
	}
	if pairings[1].Unit != nil {
		t.Errorf("pairings[1].Unit = %+v, want nil (trailing — nothing left to pair to)", pairings[1].Unit)
	}
}

func TestPairVoiceCuesTreatsEveryLaterCueAsTrailingOnceUnitsRunOut(t *testing.T) {
	units := []pairableUnit{{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}}}
	cues := []VoiceCue{
		voiceCue("cue-0", 200, 900, 500),
		voiceCue("cue-1", 5000, 5900, 500),
		voiceCue("cue-2", 6000, 6900, 500),
	}
	pairings := pairVoiceCues(cues, units)
	if len(pairings) != 3 {
		t.Fatalf("len(pairings) = %d, want 3", len(pairings))
	}
	if pairings[1].Unit != nil || pairings[2].Unit != nil {
		t.Errorf("pairings[1].Unit = %+v, pairings[2].Unit = %+v, want both nil", pairings[1].Unit, pairings[2].Unit)
	}
}

func TestPairVoiceCuesIsStrictly1To1EvenWithTwoCuesBeforeOneAction(t *testing.T) {
	units := []pairableUnit{
		{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}},
		{StartMs: 4000, EndMs: 4000, ActionIndexes: []int{1}},
	}
	cues := []VoiceCue{voiceCue("cue-0", 100, 500, 300), voiceCue("cue-1", 600, 900, 300)}
	pairings := pairVoiceCues(cues, units)
	if len(pairings) != 2 || pairings[0].Unit.StartMs != 1000 || pairings[1].Unit.StartMs != 4000 {
		t.Fatalf("pairings = %+v, want cue-0->1000 and cue-1->4000, never both to the same unit", pairings)
	}
}

// --- planVoicePlacement: sequential ceiling/floor placement + freeze insertion -----------
// (docs/plans/2026-08-21-voice-sync-and-freeze-frame.md Task 2)

// unitsOf builds the full pairable-unit sequence a set of test pairings would come from, for
// scenarios where every unit happens to be paired (no silent action in between). Tests that
// specifically need an unpaired unit build their own units slice instead. Trailing (Unit ==
// nil) pairings contribute nothing — they have no unit to begin with.
func unitsOf(pairings []voicePairing) []pairableUnit {
	var units []pairableUnit
	for _, p := range pairings {
		if p.Unit != nil {
			units = append(units, *p.Unit)
		}
	}
	return units
}

// unitsOfNotFirst is unitsOf with a dummy unpaired unit (well before source time 0, so it
// never contributes to any real floor/ceiling value — sourceTimeToOutputTime clamps anything
// at or before the timeline's own start to output 0) prepended, so the first real pairing in
// the set no longer sits at units[0]. Most placement tests want to exercise the ordinary
// next/previous-action ceiling/floor, not the first-action-in-the-whole-session exception —
// this keeps them from accidentally tripping it.
func unitsOfNotFirst(pairings []voicePairing) []pairableUnit {
	dummy := pairableUnit{StartMs: -1000, EndMs: -1000, ActionIndexes: []int{-1}}
	return append([]pairableUnit{dummy}, unitsOf(pairings)...)
}

func TestPlanVoicePlacementFitsNaturallyWithNoAdjustment(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 2000, Speed: 1}}
	pairings := []voicePairing{
		{Cue: voiceCue("cue-0", 300, 600, 250), Unit: &pairableUnit{StartMs: 500, EndMs: 500, ActionIndexes: []int{0}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})
	if len(placed) != 1 {
		t.Fatalf("len(placed) = %d, want 1", len(placed))
	}
	// Audio starts exactly where they began talking (300) and is allowed to keep running
	// 50ms into the action it explains (300+250=550 > annotatedStart 500) — overlapping the
	// cue's own action is fine, only the NEXT action is a ceiling.
	if placed[0].TMs != 300 {
		t.Errorf("TMs = %d, want 300 (unadjusted candidate start)", placed[0].TMs)
	}
	if len(timeline) != 1 {
		t.Errorf("len(timeline) = %d, want 1 (no freeze needed)", len(timeline))
	}
}

func TestPlanVoicePlacementPushesLaterToAvoidDeadAirBeforeItsOwnAction(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1}}
	pairings := []voicePairing{
		// A short 50ms cue narrating an action that only happens 2000ms later — placed
		// verbatim it would finish at 50, long before the action at 2000.
		{Cue: voiceCue("cue-0", 0, 50, 50), Unit: &pairableUnit{StartMs: 2000, EndMs: 2000, ActionIndexes: []int{0}}},
	}
	_, placed := planVoicePlacement(pairings, unitsOf(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})
	if placed[0].TMs != 1950 {
		t.Errorf("TMs = %d, want 1950 (pushed so audio ends exactly at the action, 2000)", placed[0].TMs)
	}
	if placed[0].TMs+placed[0].DurationMs != 2000 {
		t.Errorf("audio end = %d, want exactly 2000 (no dead air before its own action)", placed[0].TMs+placed[0].DurationMs)
	}
}

func TestPlanVoicePlacementPullsEarlierToAvoidTalkingOverTheNextActionNoFreezeNeeded(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	pairings := []voicePairing{
		// candidateStart=1000, tts=800 -> candidateEnd=1800, past the next action at 1500.
		// Pulling all the way back to 700 (1500-800) is still >= the floor (0), so no freeze.
		{Cue: voiceCue("cue-0", 1000, 1100, 800), Unit: &pairableUnit{StartMs: 1200, EndMs: 1200, ActionIndexes: []int{0}}},
		{Cue: voiceCue("cue-1", 1400, 1450, 100), Unit: &pairableUnit{StartMs: 1500, EndMs: 1500, ActionIndexes: []int{1}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})
	if placed[0].TMs != 700 {
		t.Errorf("TMs = %d, want 700 (pulled earlier to finish exactly at the next action, 1500)", placed[0].TMs)
	}
	if placed[0].TMs+placed[0].DurationMs != 1500 {
		t.Errorf("audio end = %d, want exactly 1500 (the next action's own start)", placed[0].TMs+placed[0].DurationMs)
	}
	if len(timeline) != 1 {
		t.Errorf("len(timeline) = %d, want 1 (pulling earlier was enough, no freeze)", len(timeline))
	}
}

func TestPlanVoicePlacementInsertsAFreezeWhenTheFloorForcesAnOverrun(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	pairings := []voicePairing{
		// A 1900ms cue starting at source 0, narrating action-0 at 100 — candidateEnd (1900)
		// would talk over action-1 at 1500, and pulling back to fit (1500-1900=-400) doesn't
		// actually avoid a freeze once the floor (0) is applied: audioStart would land before
		// action-0's own reveal (100) while a freeze is needed regardless, so it's pulled
		// forward to align with the action instead (100) rather than starting early for no
		// benefit — audioEnd lands at 2000, 500ms past action-1's natural start (1500). That
		// 500ms must become a freeze delaying action-1's own reveal.
		{Cue: voiceCue("cue-0", 0, 50, 1900), Unit: &pairableUnit{StartMs: 100, EndMs: 100, ActionIndexes: []int{0}}},
		{Cue: voiceCue("cue-1", 1400, 1450, 100), Unit: &pairableUnit{StartMs: 1500, EndMs: 1500, ActionIndexes: []int{1}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})

	if placed[0].TMs != 100 || placed[0].DurationMs != 1900 {
		t.Fatalf("placed[0] = %+v, want TMs=100 DurationMs=1900", placed[0])
	}

	preFreezeNextStart := 1500.0 // where action-1 would have landed with no freeze
	shiftedNextStart := sourceTimeToOutputTime(1500, timeline)
	if shiftedNextStart != 2000 {
		t.Errorf("action-1's output start = %v, want 2000 (shifted by exactly the 500ms freeze)", shiftedNextStart)
	}
	if got := shiftedNextStart - preFreezeNextStart; got != 500 {
		t.Errorf("shift = %v, want exactly 500 (audioEnd 2000 - unfrozen nextStartOut 1500)", got)
	}

	var holds []float64
	for _, s := range timeline {
		if s.HoldMs > 0 {
			holds = append(holds, s.HoldMs)
		}
	}
	if len(holds) != 1 || holds[0] != 500 {
		t.Errorf("held segments = %v, want exactly one with HoldMs=500", holds)
	}

	// placed[1] now floors against the freeze itself: pairing-1's own gap-fill target (no
	// dead air before its now-delayed action, 2000) coincides with the floor (cue-0's own
	// audio ending at 2000), so it starts right when the freeze ends.
	if placed[1].TMs != 2000 {
		t.Errorf("placed[1].TMs = %d, want 2000 (starts exactly when the freeze/prior audio ends)", placed[1].TMs)
	}
}

// TestPlanVoicePlacementCeilingAndFloorRespectAnUnpairedActionInBetween is the reversal of an
// earlier, explicitly recorded design decision ("ceiling/floor only look at the next/previous
// PAIRED action") — that turned out to let a cue talk straight over a silent action nobody
// was narrating. units includes unit1, which has no cue of its own; pairings only covers
// unit0 and unit2. Both halves of the fix are exercised in one scenario: cue-0's audio would
// naturally run straight through unit1 (which has no cue to protect it) if the ceiling only
// looked at the next PAIRED action (unit2) — the fix must freeze unit1's own reveal instead.
// cue-1's natural target would then land before unit1 has even finished — the fix must floor
// it against unit1's own end, not unit0's.
func TestPlanVoicePlacementCeilingAndFloorRespectAnUnpairedActionInBetween(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	// A dummy unit precedes unit0 here so THIS test exercises the ordinary next/previous-action
	// ceiling/floor, not the separate first-action-in-the-whole-session exception (covered by
	// its own test below) — unit0 is not actually first, dummy is.
	dummy := pairableUnit{StartMs: -1000, EndMs: -1000, ActionIndexes: []int{-1}}
	unit0 := pairableUnit{StartMs: 100, EndMs: 100, ActionIndexes: []int{0}}
	unit1 := pairableUnit{StartMs: 1000, EndMs: 1050, ActionIndexes: []int{1}} // no cue — silent
	unit2 := pairableUnit{StartMs: 1080, EndMs: 1080, ActionIndexes: []int{2}}
	units := []pairableUnit{dummy, unit0, unit1, unit2}
	pairings := []voicePairing{
		{Cue: voiceCue("cue-0", 0, 50, 1500), Unit: &unit0},
		{Cue: voiceCue("cue-1", 500, 550, 50), Unit: &unit2},
	}

	timeline, placed := planVoicePlacement(pairings, units, rawSegments, VoiceConfig{DefaultHoldMs: 0})

	// Ceiling: cue-0's natural 1500ms audio, starting at 0, would run to 1500 — past unit1's
	// own start (1000), which has no cue to otherwise protect it. A freeze is needed either
	// way, so the audio is aligned with its own action (unit0 at 100) instead of starting
	// early for no benefit, landing audioEnd at 1600 — a freeze of exactly 1600-1000=600ms
	// must delay unit1's own reveal, not just unit2's.
	if placed[0].TMs != 100 || placed[0].DurationMs != 1500 {
		t.Fatalf("placed[0] = %+v, want TMs=100 DurationMs=1500", placed[0])
	}
	var holds []float64
	for _, s := range timeline {
		if s.HoldMs > 0 {
			holds = append(holds, s.HoldMs)
		}
	}
	if len(holds) != 1 || holds[0] != 600 {
		t.Errorf("held segments = %v, want exactly one with HoldMs=600 (protecting unit1)", holds)
	}
	if got := sourceTimeToOutputTime(unit1.StartMs, timeline); got != 1600 {
		t.Errorf("unit1's own output start = %v, want 1600 (shifted by the freeze)", got)
	}

	// Floor: cue-1's own natural target (candidateEnd < unit2's annotated start, so it gets
	// pushed to unit2.StartOut - ttsDurationMs = 1680-50 = 1630) is still earlier than
	// unit1's own output end post-freeze (1650) — the floor must win, not fall back to
	// unit0's much earlier end.
	if placed[1].TMs != 1650 {
		t.Errorf("placed[1].TMs = %d, want 1650 (floored against unit1's own end, not unit0's)", placed[1].TMs)
	}
}

func TestPlanVoicePlacementHandlesBackToBackFreezesAcrossThreeConsecutiveCues(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	pairings := []voicePairing{
		{Cue: voiceCue("cue-0", 0, 50, 1900), Unit: &pairableUnit{StartMs: 100, EndMs: 100, ActionIndexes: []int{0}}},
		// Same shape as the single-freeze case, but cue-1's own TTS (1000ms) is long enough
		// that, once floored against cue-0's audio (ending 1900), IT also overruns action-2.
		{Cue: voiceCue("cue-1", 1400, 1450, 1000), Unit: &pairableUnit{StartMs: 1500, EndMs: 1500, ActionIndexes: []int{1}}},
		{Cue: voiceCue("cue-2", 2100, 2150, 50), Unit: &pairableUnit{StartMs: 2200, EndMs: 2200, ActionIndexes: []int{2}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})

	// cue-0 aligns with its own action (100) rather than starting early for no benefit (a
	// freeze is needed regardless), landing audioEnd at 2000. cue-1 and cue-2 are unaffected
	// by that same alignment rule — their own audioStart already lands at or past their own
	// action's reveal — so only the first placement's numbers shift from the pre-alignment
	// baseline.
	wantPlaced := []struct{ tMs, durMs int64 }{{100, 1900}, {2000, 1000}, {3000, 50}}
	if len(placed) != len(wantPlaced) {
		t.Fatalf("len(placed) = %d, want %d", len(placed), len(wantPlaced))
	}
	for i, w := range wantPlaced {
		if placed[i].TMs != w.tMs || placed[i].DurationMs != w.durMs {
			t.Errorf("placed[%d] = %+v, want TMs=%d DurationMs=%d", i, placed[i], w.tMs, w.durMs)
		}
	}

	var holds []float64
	for _, s := range timeline {
		if s.HoldMs > 0 {
			holds = append(holds, s.HoldMs)
		}
	}
	// First freeze: cue-0 (now starting at 100) overruns action-1 by 2000-1500=500. Second
	// freeze: cue-1, floored to start at 2000 (cue-0's audio end), ends at 3000 — computed
	// against the timeline the FIRST freeze already extended — and overruns action-2 (which,
	// pre-second-freeze, sits at 2000+(2200-1500)=2700) by 3000-2700=300.
	if len(holds) != 2 || holds[0] != 500 || holds[1] != 300 {
		t.Errorf("held segments = %v, want [500, 300]", holds)
	}
}

func TestPlanVoicePlacementAppliesFreezeUniformlyToATypingRunPairing(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	pairings := []voicePairing{
		// Identical numbers to TestPlanVoicePlacementInsertsAFreezeWhenTheFloorForcesAnOverrun,
		// except the paired unit spans multiple actions (a collapsed typing run) — freeze
		// insertion must not special-case that away.
		{Cue: voiceCue("cue-0", 0, 50, 1900), Unit: &pairableUnit{StartMs: 100, EndMs: 100, ActionIndexes: []int{0, 1, 2}}},
		{Cue: voiceCue("cue-1", 1400, 1450, 100), Unit: &pairableUnit{StartMs: 1500, EndMs: 1500, ActionIndexes: []int{3}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})

	if placed[0].TMs != 100 || placed[0].DurationMs != 1900 {
		t.Fatalf("placed[0] = %+v, want TMs=100 DurationMs=1900", placed[0])
	}
	var holds []float64
	for _, s := range timeline {
		if s.HoldMs > 0 {
			holds = append(holds, s.HoldMs)
		}
	}
	if len(holds) != 1 || holds[0] != 500 {
		t.Errorf("held segments = %v, want exactly one with HoldMs=500, same as the single-action case", holds)
	}
}

func TestPlanVoicePlacementLastPairingHasNoCeilingAndNoTailFreeze(t *testing.T) {
	// No pairing after this one, so there's nothing to protect from being talked over — the
	// audio is allowed to simply run past the current end of the timeline; no tail freeze is
	// invented for it.
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 200, Speed: 1}}
	pairings := []voicePairing{
		{Cue: voiceCue("cue-0", 0, 50, 10000), Unit: &pairableUnit{StartMs: 100, EndMs: 100, ActionIndexes: []int{0}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})
	if placed[0].TMs != 0 || placed[0].DurationMs != 10000 {
		t.Fatalf("placed[0] = %+v, want TMs=0 DurationMs=10000 (allowed to run past the video's own end)", placed[0])
	}
	if len(timeline) != 1 || timeline[0].HoldMs != 0 {
		t.Errorf("timeline = %+v, want the original single segment, unmodified (no tail freeze)", timeline)
	}
}

// TestPlanVoicePlacementFirstActionInTheSessionIsItsOwnCeiling covers the one exception to
// "overlapping the cue's own action is fine": the very first action in the whole session has
// nothing already on screen to justify narration bleeding into its reveal, unlike every later
// action. Deliberately uses unitsOf (not unitsOfNotFirst) — this pairing's unit really is
// units[0], nothing precedes it.
func TestPlanVoicePlacementFirstActionInTheSessionIsItsOwnCeiling(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	pairings := []voicePairing{
		// A 1500ms intro cue narrating the very first action, which happens at just 1000ms —
		// placed at its own raw start (0) it would run to 1500, 500ms past the action.
		{Cue: voiceCue("cue-0", 0, 50, 1500), Unit: &pairableUnit{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOf(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})

	if placed[0].TMs != 0 || placed[0].DurationMs != 1500 {
		t.Fatalf("placed[0] = %+v, want TMs=0 DurationMs=1500", placed[0])
	}
	var holds []float64
	for _, s := range timeline {
		if s.HoldMs > 0 {
			holds = append(holds, s.HoldMs)
		}
	}
	if len(holds) != 1 || holds[0] != 500 {
		t.Errorf("held segments = %v, want exactly one with HoldMs=500 (the action's own reveal delayed, not the next one's)", holds)
	}
	if got := sourceTimeToOutputTime(1000, timeline); got != 1500 {
		t.Errorf("the first action's own output start = %v, want 1500 (delayed until the intro finishes)", got)
	}
}

// TestPlanVoicePlacementDoesNotProtectAFirstActionThatOverlapsItsOwnCueNaturally is the
// negative case: when the intro cue's own natural timing already fits before the first
// action, the first-action ceiling exists but never triggers a freeze — it behaves exactly
// like the ordinary "no adjustment needed" case, just for units[0].
func TestPlanVoicePlacementDoesNotProtectAFirstActionThatOverlapsItsOwnCueNaturally(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1}}
	pairings := []voicePairing{
		{Cue: voiceCue("cue-0", 0, 50, 100), Unit: &pairableUnit{StartMs: 1000, EndMs: 1000, ActionIndexes: []int{0}}},
	}
	timeline, placed := planVoicePlacement(pairings, unitsOf(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})
	// Natural candidateEnd (0+100=100) is less than the action's own start (1000) — pushed
	// later so it ends exactly at 1000, same formula as the ordinary dead-air case.
	if placed[0].TMs != 900 || placed[0].DurationMs != 100 {
		t.Fatalf("placed[0] = %+v, want TMs=900 DurationMs=100", placed[0])
	}
	if len(timeline) != 1 {
		t.Errorf("len(timeline) = %d, want 1 (no freeze needed)", len(timeline))
	}
}

// TestPlanVoicePlacementPlacesTrailingCuesAfterTheLastActionAndExtendsTheTail covers narration
// with nothing left to explain (Unit == nil) — never dropped, played immediately after
// whatever came before and the video extended (a tail freeze) to fit each one, back to back.
func TestPlanVoicePlacementPlacesTrailingCuesAfterTheLastActionAndExtendsTheTail(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1}}
	unit0 := pairableUnit{StartMs: 100, EndMs: 100, ActionIndexes: []int{0}}
	pairings := []voicePairing{
		{Cue: voiceCue("cue-0", 0, 50, 50), Unit: &unit0},
		{Cue: voiceCue("cue-1", 3500, 3600, 200), Unit: nil}, // wrap-up, after everything
		{Cue: voiceCue("cue-2", 4000, 4100, 100), Unit: nil}, // a second wrap-up segment
	}
	timeline, placed := planVoicePlacement(pairings, unitsOfNotFirst(pairings), rawSegments, VoiceConfig{DefaultHoldMs: 0})

	if len(placed) != 3 {
		t.Fatalf("len(placed) = %d, want 3 — trailing cues must not be dropped", len(placed))
	}
	// cue-0 is ordinary (unit0 is not first here — unitsOfNotFirst — so no special ceiling);
	// it's short and fits with room to spare before the video's own natural end (3000).
	if placed[0].TMs != 50 || placed[0].DurationMs != 50 {
		t.Fatalf("placed[0] = %+v, want TMs=50 DurationMs=50", placed[0])
	}
	// cue-1 starts exactly where the video currently ends (3000) and extends it by its own
	// full length (200ms) via a tail freeze, since there's no real footage left to trim from.
	if placed[1].TMs != 3000 || placed[1].DurationMs != 200 {
		t.Fatalf("placed[1] = %+v, want TMs=3000 DurationMs=200", placed[1])
	}
	// cue-2 stacks right after cue-1 (3200), extending the tail again.
	if placed[2].TMs != 3200 || placed[2].DurationMs != 100 {
		t.Fatalf("placed[2] = %+v, want TMs=3200 DurationMs=100", placed[2])
	}
	if got := outputDurationMs(timeline); got != 3300 {
		t.Errorf("final video duration = %v, want 3300 (3000 + 200 + 100 of tail freeze)", got)
	}
	var holds []float64
	for _, s := range timeline {
		if s.HoldMs > 0 {
			holds = append(holds, s.HoldMs)
		}
	}
	if len(holds) != 2 || holds[0] != 200 || holds[1] != 100 {
		t.Errorf("held segments = %v, want [200, 100] — one tail freeze per trailing cue", holds)
	}
}

func TestPlanVoicePlacementWithNoPairingsReturnsTheSameTimelineBuildTimelineWouldAlone(t *testing.T) {
	rawSegments := []RawSegment{{SourceStartMs: 0, SourceEndMs: 1000, Speed: 1}}
	want := buildTimeline(rawSegments)
	timeline, placed := planVoicePlacement(nil, nil, rawSegments, DefaultConfig().Voice)
	if len(placed) != 0 {
		t.Errorf("len(placed) = %d, want 0 for no pairings", len(placed))
	}
	if len(timeline) != len(want) {
		t.Fatalf("len(timeline) = %d, want %d", len(timeline), len(want))
	}
	for i := range want {
		if timeline[i] != want[i] {
			t.Errorf("timeline[%d] = %+v, want %+v", i, timeline[i], want[i])
		}
	}
}

// --- Regression: the whole voice pipeline must be inert with no voice data --------------

func TestDirectWithNoVoiceCuesLeavesTimelineAndSegmentsUnchanged(t *testing.T) {
	target := Target{Role: "textbox", Label: "Name"}
	session := Session{
		DurationMs: 6000,
		Viewport:   Viewport{Width: 1000, Height: 800, DevicePixelRatio: 1},
		Events: []Event{
			{Kind: "input", T: 100, Target: &target},
			{Kind: "input", T: 200, Target: &target},
			{Kind: "click", T: 3000, X: new(10.0), Y: new(20.0)},
		},
	}
	withoutVoice := Direct(session)

	session.VoiceCues = nil // explicit: the pre-feature, byte-identical path
	withoutVoiceExplicitNil := Direct(session)

	if len(withoutVoice.Voice) != 0 {
		t.Errorf("Project.Voice = %v, want empty with no voice cues", withoutVoice.Voice)
	}
	if len(withoutVoice.Timeline) != len(withoutVoiceExplicitNil.Timeline) {
		t.Fatalf("timeline length changed between nil and omitted VoiceCues")
	}
	for i := range withoutVoice.Timeline {
		if withoutVoice.Timeline[i] != withoutVoiceExplicitNil.Timeline[i] {
			t.Errorf("Timeline[%d] = %+v, want %+v", i, withoutVoiceExplicitNil.Timeline[i], withoutVoice.Timeline[i])
		}
	}
}

// TestDirectLeavesAnUnpairedActionsGapExactlyAsToday is Task 10's acceptance criterion "an
// action with no paired cue is scheduled exactly as it is today", tested against a MIXED
// session (one action paired, one not) rather than an all-or-nothing one — the stronger claim
// is that the *unpaired* action's own gap is untouched even while a sibling action in the same
// session has a voice cue, not merely that an all-voiceless session is unaffected.
func TestDirectLeavesAnUnpairedActionsGapExactlyAsToday(t *testing.T) {
	baseSession := Session{
		DurationMs: 8000,
		Viewport:   Viewport{Width: 1000, Height: 800, DevicePixelRatio: 1},
		Events: []Event{
			{Kind: "click", T: 1000, X: new(10.0), Y: new(20.0)},
			{Kind: "click", T: 6000, X: new(30.0), Y: new(40.0)}, // never gets a cue below
		},
	}

	withoutVoice := Direct(baseSession)

	withVoice := baseSession
	withVoice.VoiceCues = []VoiceCue{voiceCue("cue-0", 200, 900, 500)} // pairs only to the first click
	withVoiceProject := Direct(withVoice)

	if len(withVoiceProject.Voice) != 1 {
		t.Fatalf("len(Voice) = %d, want 1 (only the first click is paired)", len(withVoiceProject.Voice))
	}
	// The gap around the SECOND (unpaired) click — and everything from there to the tail —
	// must be scheduled identically whether or not the first click has a voice cue.
	if len(withoutVoice.Timeline) != len(withVoiceProject.Timeline) {
		t.Fatalf("timeline length differs: %d vs %d", len(withoutVoice.Timeline), len(withVoiceProject.Timeline))
	}
	for i := range withoutVoice.Timeline {
		if withoutVoice.Timeline[i] != withVoiceProject.Timeline[i] {
			t.Errorf("Timeline[%d] = %+v, want %+v (unpaired action's scheduling changed)", i, withVoiceProject.Timeline[i], withoutVoice.Timeline[i])
		}
	}
	if withoutVoice.DurationMs != withVoiceProject.DurationMs {
		t.Errorf("DurationMs = %d, want %d", withVoiceProject.DurationMs, withoutVoice.DurationMs)
	}
}

// TestDirectSyncsOverlappingCuesAndAFreezeEndToEnd is
// docs/plans/2026-08-21-voice-sync-and-freeze-frame.md Task 5's end-to-end regression: both
// symptoms from the original bug report, through the real Direct() pipeline (planSegments'
// pause/gap classification included, not just the low-level planVoicePlacement unit tests
// above) — a cue whose audio would naturally overlap the next cue's, with a TTSDurationMs
// long enough to also force a freeze against the second action. Exact tMs values depend on
// planSegments' own gap compression, so this asserts the invariants the bug report cared
// about rather than pinned numbers.
func TestDirectSyncsOverlappingCuesAndAFreezeEndToEnd(t *testing.T) {
	session := Session{
		DurationMs: 3000,
		Viewport:   Viewport{Width: 1000, Height: 800, DevicePixelRatio: 1},
		Events: []Event{
			clickEvent(100, 10, 20),
			clickEvent(1500, 30, 40),
		},
		VoiceCues: []VoiceCue{
			voiceCue("cue-0", 0, 50, 5000), // deliberately far longer than any real gap here
			voiceCue("cue-1", 1400, 1450, 100),
		},
	}
	project := Direct(session)

	if len(project.Voice) != 2 {
		t.Fatalf("len(project.Voice) = %d, want 2", len(project.Voice))
	}
	cue0End := project.Voice[0].TMs + project.Voice[0].DurationMs
	if project.Voice[1].TMs < cue0End {
		t.Errorf("cue-1.TMs = %d, want >= %d (cue-0's own audio end) — cues overlap", project.Voice[1].TMs, cue0End)
	}

	var froze bool
	for _, s := range project.Timeline {
		if s.HoldMs > 0 {
			froze = true
		}
	}
	if !froze {
		t.Error("want at least one held (freeze) segment in Project.Timeline — cue-0's audio should have forced one")
	}

	if len(project.Cursor.Clicks) != 2 {
		t.Fatalf("len(project.Cursor.Clicks) = %d, want 2", len(project.Cursor.Clicks))
	}
	if got := project.Cursor.Clicks[1].TMs; got < float64(cue0End) {
		t.Errorf("second click's own reveal (tMs=%v) happens before cue-0's audio finishes (%d) — narration and action desynced", got, cue0End)
	}
}

func TestDirectPlacesAKeptCueAndSkipsADroppedOne(t *testing.T) {
	session := Session{
		DurationMs: 6000,
		Viewport:   Viewport{Width: 1000, Height: 800, DevicePixelRatio: 1},
		Events: []Event{
			{Kind: "click", T: 1000, X: new(10.0), Y: new(20.0)},
			{Kind: "click", T: 4000, X: new(30.0), Y: new(40.0)},
		},
		VoiceCues: []VoiceCue{
			voiceCue("cue-0", 200, 900, 500),
			{ID: "cue-1", SourceStartMs: 1200, SourceEndMs: 1800, RewrittenText: nil}, // dropped
		},
	}
	project := Direct(session)
	if len(project.Voice) != 1 {
		t.Fatalf("len(project.Voice) = %d, want 1 (dropped cue must not appear)", len(project.Voice))
	}
	if project.Voice[0].AudioFile != "voice/cue-0.mp3" {
		t.Errorf("project.Voice[0].AudioFile = %q, want voice/cue-0.mp3", project.Voice[0].AudioFile)
	}
}
