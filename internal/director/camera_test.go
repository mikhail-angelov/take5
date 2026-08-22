package director

import "testing"

// Port of test/camera.test.js.

var testViewport = Viewport{Width: 1440, Height: 900, DevicePixelRatio: 2}

func clickEventRect(t, x, y float64, rect Rect, label string) Event {
	e := clickEvent(t, x, y)
	e.Target = &Target{Role: "button", Rect: &rect, Label: label}
	return e
}

func planForCamera(events []Event, durationMs float64) ([]CameraKeyframe, []Segment) {
	if durationMs == 0 {
		durationMs = events[len(events)-1].T + 1000
	}
	actions := meaningfulActions(events)
	units := computePairableUnits(actions)
	timeline := buildTimeline(planSegments(units, durationMs, nil, testConfig).Segments)
	return planCamera(actions, timeline, testViewport, testConfig), timeline
}

func TestZoomForTargetLeavesALargeObviousTargetAlone(t *testing.T) {
	if z := zoomForTarget(Rect{X: 200, Y: 300, Width: 900, Height: 300}, "button", testViewport, testConfig.Camera); z != 1 {
		t.Errorf("zoom = %v, want 1", z)
	}
}

func TestZoomForTargetZoomsAMediumTargetGently(t *testing.T) {
	if z := zoomForTarget(Rect{X: 200, Y: 300, Width: 400, Height: 60}, "button", testViewport, testConfig.Camera); z != 1.15 {
		t.Errorf("zoom = %v, want 1.15", z)
	}
}

func TestZoomForTargetZoomsASmallIconTargetTheMost(t *testing.T) {
	if z := zoomForTarget(Rect{X: 1200, Y: 40, Width: 32, Height: 32}, "button", testViewport, testConfig.Camera); z != 1.35 {
		t.Errorf("zoom = %v, want 1.35", z)
	}
}

func TestZoomForTargetKeepsTextEntryWithinItsGentlerRange(t *testing.T) {
	zoom := zoomForTarget(Rect{X: 400, Y: 240, Width: 100, Height: 30}, "textbox", testViewport, testConfig.Camera)
	if !(zoom >= 1.15 && zoom <= 1.25) {
		t.Errorf("zoom = %v, want within [1.15, 1.25]", zoom)
	}
}

func TestZoomForTargetNeverExceedsTheHardMaximum(t *testing.T) {
	for _, size := range []float64{8, 16, 24, 40, 120, 400, 1200} {
		zoom := zoomForTarget(Rect{X: 0, Y: 0, Width: size, Height: size}, "button", testViewport, testConfig.Camera)
		if zoom > testConfig.Camera.MaxZoom {
			t.Errorf("size %v produced %v", size, zoom)
		}
	}
}

func TestPlanCameraProducesNoCameraMovesForLargeObviousTargets(t *testing.T) {
	big := Rect{X: 200, Y: 300, Width: 900, Height: 300}
	camera, _ := planForCamera([]Event{
		clickEventRect(1000, 700, 450, big, "Generate"),
		clickEventRect(3000, 700, 450, big, "Save"),
	}, 0)
	if len(camera) != 0 {
		t.Errorf("camera = %+v, want empty", camera)
	}
}

func TestPlanCameraZoomsInAndBackOutForASingleSmallTarget(t *testing.T) {
	camera, _ := planForCamera([]Event{
		clickEventRect(2000, 1216, 56, Rect{X: 1200, Y: 40, Width: 32, Height: 32}, "Settings"),
	}, 0)
	if len(camera) != 4 {
		t.Fatalf("len(camera) = %d, want 4", len(camera))
	}
	if camera[0].Zoom != 1 {
		t.Errorf("camera[0].Zoom = %v, want 1", camera[0].Zoom)
	}
	if !(camera[1].Zoom > 1) {
		t.Errorf("camera[1].Zoom = %v, want > 1", camera[1].Zoom)
	}
	if camera[2].Zoom != camera[1].Zoom {
		t.Errorf("camera[2].Zoom = %v, want %v", camera[2].Zoom, camera[1].Zoom)
	}
	if camera[3].Zoom != 1 {
		t.Errorf("camera[3].Zoom = %v, want 1", camera[3].Zoom)
	}
}

func TestPlanCameraKeepsNearbyConsecutiveTargetsInOneShotWithoutFlicker(t *testing.T) {
	camera, _ := planForCamera([]Event{
		clickEventRect(2000, 430, 260, Rect{X: 400, Y: 240, Width: 60, Height: 40}, "Name"),
		clickEventRect(2800, 470, 320, Rect{X: 440, Y: 300, Width: 60, Height: 40}, "Create"),
		clickEventRect(3600, 510, 380, Rect{X: 480, Y: 360, Width: 60, Height: 40}, "Confirm"),
	}, 0)

	var neutral []CameraKeyframe
	for _, k := range camera {
		if k.Zoom == 1 {
			neutral = append(neutral, k)
		}
	}
	if len(neutral) != 2 {
		t.Fatalf("len(neutral) = %d, want 2", len(neutral))
	}
	if neutral[0].TMs != camera[0].TMs {
		t.Error("first neutral keyframe should be the first keyframe")
	}
	if neutral[1].TMs != camera[len(camera)-1].TMs {
		t.Error("last neutral keyframe should be the last keyframe")
	}
}

func TestPlanCameraPansBetweenSpatiallyDistantTargetsRatherThanZoomingOut(t *testing.T) {
	camera, _ := planForCamera([]Event{
		clickEventRect(2000, 100, 100, Rect{X: 80, Y: 80, Width: 40, Height: 40}, "Left"),
		clickEventRect(2600, 1340, 820, Rect{X: 1320, Y: 800, Width: 40, Height: 40}, "Right"),
	}, 0)

	var neutralCount int
	distinctCx := map[float64]bool{}
	for _, k := range camera {
		if k.Zoom == 1 {
			neutralCount++
		} else {
			distinctCx[float64(int(k.Cx+0.5))] = true
		}
	}
	if neutralCount != 2 {
		t.Errorf("neutralCount = %d, want 2 (should not return to 1x between two close-in-time shots)", neutralCount)
	}
	if len(distinctCx) <= 1 {
		t.Error("camera should pan between the two targets")
	}
}

func TestPlanCameraReturnsToNeutralBetweenShotsSeparatedByUnzoomedWork(t *testing.T) {
	big := Rect{X: 200, Y: 300, Width: 900, Height: 300}
	camera, _ := planForCamera([]Event{
		clickEventRect(2000, 100, 100, Rect{X: 80, Y: 80, Width: 40, Height: 40}, "Left"),
		clickEventRect(2400, 140, 140, Rect{X: 120, Y: 120, Width: 40, Height: 40}, "Also left"),
		clickEventRect(3200, 700, 450, big, "Run"),
		clickEventRect(4000, 700, 450, big, "Run"),
		clickEventRect(4800, 700, 450, big, "Run"),
		clickEventRect(5600, 700, 450, big, "Run"),
		clickEventRect(6400, 1340, 820, Rect{X: 1320, Y: 800, Width: 40, Height: 40}, "Right"),
	}, 8000)

	count := 0
	for _, k := range camera {
		if k.Zoom == 1 {
			count++
		}
	}
	if count != 4 {
		t.Errorf("neutral count = %d, want 4", count)
	}
}

func TestPlanCameraKeepsTheCropWindowInsideTheFrame(t *testing.T) {
	camera, _ := planForCamera([]Event{
		clickEventRect(2000, 20, 20, Rect{X: 0, Y: 0, Width: 40, Height: 40}, "Corner"),
	}, 0)
	for _, kf := range camera {
		halfWidth := testViewport.Width / (2 * kf.Zoom)
		halfHeight := testViewport.Height / (2 * kf.Zoom)
		if kf.Cx < halfWidth-0.001 {
			t.Errorf("cx = %v below half-width bound %v", kf.Cx, halfWidth)
		}
		if kf.Cy < halfHeight-0.001 {
			t.Errorf("cy = %v below half-height bound %v", kf.Cy, halfHeight)
		}
		if kf.Cx > testViewport.Width-halfWidth+0.001 {
			t.Errorf("cx = %v above bound", kf.Cx)
		}
	}
}

func TestPlanCameraEmitsStrictlyIncreasingKeyframeTimes(t *testing.T) {
	camera, _ := planForCamera([]Event{
		clickEventRect(1200, 100, 100, Rect{X: 80, Y: 80, Width: 40, Height: 40}, ""),
		clickEventRect(1300, 1340, 820, Rect{X: 1320, Y: 800, Width: 40, Height: 40}, ""),
		clickEventRect(1400, 100, 820, Rect{X: 80, Y: 800, Width: 40, Height: 40}, ""),
	}, 0)
	for i := 1; i < len(camera); i++ {
		if !(camera[i].TMs > camera[i-1].TMs) {
			t.Errorf("keyframe %d.TMs = %v does not exceed keyframe %d.TMs = %v", i, camera[i].TMs, i-1, camera[i-1].TMs)
		}
	}
}

func TestPlanCameraStartsTheCameraMoveBeforeTheActionItIsHeadingFor(t *testing.T) {
	camera, timeline := planForCamera([]Event{
		clickEventRect(2000, 1216, 56, Rect{X: 1200, Y: 40, Width: 32, Height: 32}, "Settings"),
	}, 0)
	actionOutputMs := camera[1].TMs + testConfig.Camera.LeadMs
	// The evaluation of this plan at an output instant (interpolating/easing between
	// keyframes) is internal/render's concern now, not Director's — see
	// internal/render/evaluate_test.go for the keyframe-sampling tests. Here we only need
	// the structural guarantee that by actionOutputMs the shot has already settled: it falls
	// within the fully-zoomed [camera[1], camera[2]] window, and that window's zoom doesn't
	// change over time.
	if !(actionOutputMs >= camera[1].TMs && actionOutputMs <= camera[2].TMs) {
		t.Errorf("actionOutputMs = %v, want within [%v, %v]", actionOutputMs, camera[1].TMs, camera[2].TMs)
	}
	if camera[1].Zoom != camera[2].Zoom {
		t.Errorf("camera[1].Zoom = %v, camera[2].Zoom = %v, want equal (already settled)", camera[1].Zoom, camera[2].Zoom)
	}
	if len(timeline) == 0 {
		t.Error("expected a non-empty timeline")
	}
}

// TestPlanCameraNeedsNoCodeChangeForAFreeze locks in
// docs/plans/2026-08-21-voice-sync-and-freeze-frame.md Task 4's claim: planCamera only ever
// calls sourceTimeToOutputTime on action instants, so a held segment inserted between two
// widely-separated targets shifts every keyframe tied to the SECOND target by exactly
// HoldMs and leaves keyframes tied to the first one untouched, with zero changes to
// camera.go itself.
func TestPlanCameraNeedsNoCodeChangeForAFreeze(t *testing.T) {
	rectA := Rect{X: 100, Y: 100, Width: 40, Height: 40}
	rectB := Rect{X: 900, Y: 700, Width: 40, Height: 40}
	actions := []Action{
		{Kind: "click", StartMs: 500, EndMs: 500, Event: Event{Kind: "click", Target: &Target{Role: "button", Rect: &rectA, Label: "A"}}},
		{Kind: "click", StartMs: 2000, EndMs: 2000, Event: Event{Kind: "click", Target: &Target{Role: "button", Rect: &rectB, Label: "B"}}},
	}
	noHold := buildTimeline([]RawSegment{{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1}})
	withHold := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1},
		{SourceStartMs: 1000, SourceEndMs: 1000, HoldMs: 500},
	})

	before := planCamera(actions, noHold, testViewport, testConfig)
	after := planCamera(actions, withHold, testViewport, testConfig)
	if len(before) == 0 || len(before) != len(after) {
		t.Fatalf("before = %+v, after = %+v, want equal non-zero keyframe counts", before, after)
	}

	// Only two invariants are safe to assert from outside camera.go's own shot-window
	// clipping (shotRange's min()/max() clamp a shot's trailing hold against the next
	// shot's start, so an interior keyframe's TMs need not be a simple function of "which
	// action instant does this belong to" — that's shot A's OWN geometry, legitimately
	// independent of B's timing, not a bug): the very first keyframe is causally upstream of
	// the freeze and must be untouched, and the very last one is causally downstream of
	// target B (the last action) and must shift by exactly HoldMs.
	last := len(before) - 1
	if before[0].TMs != after[0].TMs {
		t.Errorf("first keyframe TMs = %v, want unchanged at %v", after[0].TMs, before[0].TMs)
	}
	if got := after[last].TMs - before[last].TMs; got != 500 {
		t.Errorf("last keyframe shifted by %v, want exactly 500 (the HoldMs)", got)
	}
}
