package render

import (
	"strings"
	"testing"

	"take5/internal/director"
)

func TestCursorEventsUseGrabbingCursorWhileHeld(t *testing.T) {
	start := director.CursorPoint{X: 100, Y: 50}
	cursor := director.CursorPlan{
		Start: &start,
		Holds: []director.CursorHold{{StartMs: 100, EndMs: 200}},
	}

	events := cursorEvents(cursor, func(_ float64, x, y float64) (float64, float64) {
		return x, y
	}, cursorShapeHeight, 100, 300)
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}

	arrow := drawing(cursorShape, 1)
	grabbing := drawing(grabbingCursorShape, 1)
	if !strings.Contains(events[0], arrow) || !strings.Contains(events[1], grabbing) || !strings.Contains(events[2], arrow) {
		t.Errorf("cursor shapes = %q, want arrow, grabbing, arrow", events)
	}
}
