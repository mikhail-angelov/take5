package render

import (
	"math"
	"testing"

	"take5/internal/director"
)

// Sampling a Director-produced plan (camera keyframes, cursor moves/holds) at an output
// instant is render's own concern — see internal/render/evaluate.go. These tests moved here
// from internal/director/camera_test.go and cursor_test.go when the evaluation functions did.

var evalViewport = director.Viewport{Width: 1440, Height: 900, DevicePixelRatio: 2}

func testCameraKeyframes() []director.CameraKeyframe {
	return []director.CameraKeyframe{
		{TMs: 0, Zoom: 1, Cx: 720, Cy: 450},
		{TMs: 1000, Zoom: 1.3, Cx: 300, Cy: 200},
		{TMs: 2000, Zoom: 1.3, Cx: 300, Cy: 200},
	}
}

func TestCameraStateAtHoldsFirstAndLastValuesOutsideKeyframeRange(t *testing.T) {
	camera := testCameraKeyframes()
	if got := cameraStateAt(camera, -500, evalViewport); got != camera[0] {
		t.Errorf("at -500 = %+v, want %+v", got, camera[0])
	}
	if got := cameraStateAt(camera, 9000, evalViewport); got != camera[2] {
		t.Errorf("at 9000 = %+v, want %+v", got, camera[2])
	}
}

func TestCameraStateAtEasesRatherThanJumpingBetweenKeyframes(t *testing.T) {
	camera := testCameraKeyframes()
	quarter := cameraStateAt(camera, 250, evalViewport)
	half := cameraStateAt(camera, 500, evalViewport)
	if half.Zoom != 1.15 {
		t.Errorf("half.Zoom = %v, want 1.15 (midpoint of ease-in-out is the midpoint value)", half.Zoom)
	}
	if !(quarter.Zoom < half.Zoom) {
		t.Error("quarter.Zoom should be less than half.Zoom")
	}
	if !(quarter.Zoom-1 < (half.Zoom-1)/2) {
		t.Error("should start slowly")
	}
}

func TestCameraStateAtIsNeutralWhenThereIsNoCameraPlan(t *testing.T) {
	got := cameraStateAt(nil, 500, evalViewport)
	if got.Zoom != 1 || got.Cx != 720 || got.Cy != 450 {
		t.Errorf("got %+v, want {Zoom:1 Cx:720 Cy:450}", got)
	}
}

func testCursorPlan() director.CursorPlan {
	start := director.CursorPoint{X: 0, Y: 0}
	return director.CursorPlan{
		Start: &start,
		Moves: []director.CursorMove{{
			StartMs: 1000, EndMs: 1400,
			From: director.CursorPoint{X: 0, Y: 0}, To: director.CursorPoint{X: 400, Y: 200}, Ease: "cubic",
		}},
	}
}

func TestCursorPositionAtHoldsStartPositionBeforeFirstMove(t *testing.T) {
	plan := testCursorPlan()
	if *cursorPositionAt(plan, 0) != (director.CursorPoint{X: 0, Y: 0}) {
		t.Error("expected start position at t=0")
	}
	if *cursorPositionAt(plan, 1000) != (director.CursorPoint{X: 0, Y: 0}) {
		t.Error("expected start position at t=1000")
	}
}

func TestCursorPositionAtHoldsDestinationAfterMove(t *testing.T) {
	plan := testCursorPlan()
	if *cursorPositionAt(plan, 1400) != (director.CursorPoint{X: 400, Y: 200}) {
		t.Error("expected destination at t=1400")
	}
	if *cursorPositionAt(plan, 9000) != (director.CursorPoint{X: 400, Y: 200}) {
		t.Error("expected destination at t=9000")
	}
}

func TestCursorPositionAtEasesThroughTheMiddleOfAMove(t *testing.T) {
	plan := testCursorPlan()
	if *cursorPositionAt(plan, 1200) != (director.CursorPoint{X: 200, Y: 100}) {
		t.Errorf("midpoint = %+v, want {200 100}", cursorPositionAt(plan, 1200))
	}
	quarter := cursorPositionAt(plan, 1100)
	if !(quarter.X < 100) {
		t.Error("an ease-in should lag a linear interpolation early on")
	}
}

func TestCursorHeldAtCoversAHalfOpenInterval(t *testing.T) {
	plan := director.CursorPlan{Holds: []director.CursorHold{{StartMs: 2000, EndMs: 2300}}}
	if !cursorHeldAt(plan, 2000) {
		t.Error("expected held at the start of the interval")
	}
	if !cursorHeldAt(plan, 2150) {
		t.Error("expected held in the middle of the interval")
	}
	if cursorHeldAt(plan, 2300) {
		t.Error("expected not held at the (exclusive) end of the interval")
	}
}

func TestEaseInOutCubicIsAnchoredAndSymmetric(t *testing.T) {
	if easeInOutCubic(0) != 0 {
		t.Error("easeInOutCubic(0) != 0")
	}
	if easeInOutCubic(1) != 1 {
		t.Error("easeInOutCubic(1) != 1")
	}
	if easeInOutCubic(0.5) != 0.5 {
		t.Error("easeInOutCubic(0.5) != 0.5")
	}
	if math.Abs(easeInOutCubic(0.25)+easeInOutCubic(0.75)-1) >= 1e-12 {
		t.Error("not symmetric about the midpoint")
	}
}
