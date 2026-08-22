package director

import (
	"testing"
)

// Port of test/cursor.test.js.

func dragEvent(startMs, endMs float64, path []PathPoint) Event {
	last := path[len(path)-1]
	return Event{Kind: "drag", T: startMs, StartMs: new(startMs), EndMs: new(endMs), X: new(last.X), Y: new(last.Y), Path: path}
}

func inputEventRect(t float64, label string, rect Rect) Event {
	return Event{Kind: "input", T: t, Target: &Target{Role: "textbox", Label: label, Rect: &rect}}
}

func planForCursor(events []Event) (CursorPlan, []Segment) {
	actions := meaningfulActions(events)
	units := computePairableUnits(actions)
	timeline := buildTimeline(planSegments(units, 20000, nil, testConfig).Segments)
	return planCursor(actions, timeline, testConfig), timeline
}

func TestPlanCursorIgnoresRawPointerWanderingEntirely(t *testing.T) {
	events := make([]Event, 1, 42)
	events[0] = clickEvent(900, 100, 100)
	for i := range 40 {
		events = append(events, pointerEvent(1000+float64(i)*50, 200+float64(i)*17, 400+float64(i%7)*23))
	}
	events = append(events, clickEvent(3200, 800, 600))

	plan, _ := planForCursor(events)
	if len(plan.Moves) != 1 {
		t.Fatalf("len(moves) = %d, want 1", len(plan.Moves))
	}
	if *plan.Start != (CursorPoint{X: 100, Y: 100}) {
		t.Errorf("start = %+v", plan.Start)
	}
	if plan.Moves[0].To != (CursorPoint{X: 800, Y: 600}) {
		t.Errorf("moves[0].To = %+v", plan.Moves[0].To)
	}
}

func TestPlanCursorScalesMovementDurationWithDistanceWithinBounds(t *testing.T) {
	near, _ := planForCursor([]Event{clickEvent(1000, 100, 100), clickEvent(1700, 130, 120)})
	far, _ := planForCursor([]Event{clickEvent(1000, 40, 40), clickEvent(1700, 1400, 860)})

	nearMs := near.Moves[0].EndMs - near.Moves[0].StartMs
	farMs := far.Moves[0].EndMs - far.Moves[0].StartMs
	if nearMs != testConfig.Cursor.MinMoveMs {
		t.Errorf("nearMs = %v, want %v", nearMs, testConfig.Cursor.MinMoveMs)
	}
	if farMs != testConfig.Cursor.MaxMoveMs {
		t.Errorf("farMs = %v, want %v", farMs, testConfig.Cursor.MaxMoveMs)
	}
	if !(nearMs < farMs) {
		t.Error("nearMs should be less than farMs")
	}
}

func TestPlanCursorArrivesBeforeTheClickSoThePointerIsSettled(t *testing.T) {
	plan, timeline := planForCursor([]Event{clickEvent(1000, 100, 100), clickEvent(3000, 900, 500)})
	clickOutMs := sourceTimeToOutputTime(3000, timeline)
	if plan.Moves[0].EndMs != clickOutMs-testConfig.Cursor.SettleMs {
		t.Errorf("moves[0].EndMs = %v, want %v", plan.Moves[0].EndMs, clickOutMs-testConfig.Cursor.SettleMs)
	}
	// Sampling this plan at an output instant is internal/render's concern now — see
	// internal/render/evaluate_test.go — so here we only check the move's destination lands
	// exactly on the click.
	if plan.Moves[0].To != (CursorPoint{X: 900, Y: 500}) {
		t.Errorf("moves[0].To = %+v, want {900 500}", plan.Moves[0].To)
	}
}

func TestPlanCursorNeverStartsAMoveBeforeThePreviousTargetWasReached(t *testing.T) {
	plan, _ := planForCursor([]Event{
		clickEvent(1000, 100, 100), clickEvent(1200, 1400, 800), clickEvent(1400, 200, 700),
	})
	for i := 1; i < len(plan.Moves); i++ {
		if plan.Moves[i].StartMs < plan.Moves[i-1].EndMs {
			t.Errorf("move %d starts before move %d ends", i, i-1)
		}
	}
}

func TestPlanCursorAimsAtCentreOfInputTargetWithNoPointerCoordinates(t *testing.T) {
	plan, _ := planForCursor([]Event{
		clickEvent(1000, 100, 100),
		inputEventRect(2500, "Project name", Rect{X: 420, Y: 240, Width: 380, Height: 42}),
	})
	if plan.Moves[0].To != (CursorPoint{X: 610, Y: 261}) {
		t.Errorf("moves[0].To = %+v, want {610 261}", plan.Moves[0].To)
	}
}

func TestPlanCursorFollowsTheRealPathOfADrag(t *testing.T) {
	path := []PathPoint{
		{T: 2000, X: 200, Y: 200}, {T: 2100, X: 260, Y: 210},
		{T: 2200, X: 320, Y: 260}, {T: 2300, X: 380, Y: 300},
	}
	plan, _ := planForCursor([]Event{clickEvent(1000, 100, 100), dragEvent(2000, 2300, path)})

	var linear, cubic []CursorMove
	for _, m := range plan.Moves {
		if m.Ease == "linear" {
			linear = append(linear, m)
		} else {
			cubic = append(cubic, m)
		}
	}
	if len(linear) != len(path)-1 {
		t.Errorf("len(linear) = %d, want %d", len(linear), len(path)-1)
	}
	if linear[len(linear)-1].To != (CursorPoint{X: 380, Y: 300}) {
		t.Errorf("last linear move.To = %+v", linear[len(linear)-1].To)
	}
	// Drag geometry is preserved, so the approach to the drag start is the only easing.
	if len(cubic) != 1 {
		t.Errorf("len(cubic) = %d, want 1", len(cubic))
	}
	if len(plan.Holds) != 1 {
		t.Fatalf("len(holds) = %d, want 1", len(plan.Holds))
	}
	// Whether an instant falls inside the [start, end) hold interval is internal/render's
	// concern now — see internal/render/evaluate_test.go — so here we only check the
	// interval itself is non-empty.
	if !(plan.Holds[0].EndMs > plan.Holds[0].StartMs) {
		t.Errorf("hold should be non-empty: [%v, %v)", plan.Holds[0].StartMs, plan.Holds[0].EndMs)
	}
}

func TestPlanCursorRecordsAClickPositionForEveryClick(t *testing.T) {
	plan, _ := planForCursor([]Event{
		clickEvent(1000, 100, 100), clickEvent(2000, 300, 300),
		inputEventRect(3000, "Name", Rect{X: 0, Y: 0, Width: 100, Height: 40}),
	})
	if len(plan.Clicks) != 2 {
		t.Errorf("len(clicks) = %d, want 2", len(plan.Clicks))
	}
}

func TestPlanCursorReturnsAnEmptyPlanWhenNothingHasAPosition(t *testing.T) {
	plan, _ := planForCursor([]Event{{Kind: "shortcut", T: 1000, Key: "Meta+K"}})
	if plan.Start != nil {
		t.Errorf("start = %+v, want nil", plan.Start)
	}
	if len(plan.Moves) != 0 || len(plan.Clicks) != 0 {
		t.Errorf("moves/clicks not empty: %+v", plan)
	}
}

// TestPlanCursorNeedsNoCodeChangeForAFreeze locks in
// docs/plans/2026-08-21-voice-sync-and-freeze-frame.md Task 4's claim: planCursor only ever
// calls sourceTimeToOutputTime on action instants, so a held segment inserted between two
// clicks shifts the SECOND click's tMs by exactly HoldMs and leaves the first one untouched,
// with zero changes to cursor.go itself.
func TestPlanCursorNeedsNoCodeChangeForAFreeze(t *testing.T) {
	actions := []Action{
		{Kind: "click", StartMs: 500, EndMs: 500, Event: Event{Kind: "click", X: new(100.0), Y: new(100.0)}},
		{Kind: "click", StartMs: 2000, EndMs: 2000, Event: Event{Kind: "click", X: new(300.0), Y: new(300.0)}},
	}
	noHold := buildTimeline([]RawSegment{{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1}})
	withHold := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1},
		{SourceStartMs: 1000, SourceEndMs: 1000, HoldMs: 500},
	})

	before := planCursor(actions, noHold, testConfig)
	after := planCursor(actions, withHold, testConfig)
	if len(before.Clicks) != 2 || len(after.Clicks) != 2 {
		t.Fatalf("before = %+v, after = %+v, want 2 clicks each", before.Clicks, after.Clicks)
	}
	if before.Clicks[0].TMs != after.Clicks[0].TMs {
		t.Errorf("click before the freeze: TMs = %v, want unchanged at %v", after.Clicks[0].TMs, before.Clicks[0].TMs)
	}
	if got := after.Clicks[1].TMs - before.Clicks[1].TMs; got != 500 {
		t.Errorf("click after the freeze shifted by %v, want exactly 500 (the HoldMs)", got)
	}
}
