package director

import "math"

// Shared geometry helpers for the action targets recorded by the content script.
// All coordinates are CSS pixels relative to the viewport, as captured.
// Direct port of src/director/targets.js.

func targetRect(action Action) *Rect {
	if action.Event.Target == nil || action.Event.Target.Rect == nil {
		return nil
	}
	r := action.Event.Target.Rect
	if r.Width <= 0 || r.Height <= 0 {
		return nil
	}
	return r
}

func rectCenter(r Rect) Point {
	return Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
}

// actionPoint is where the synthetic cursor should point for this action.
func actionPoint(action Action) *Point {
	if action.Event.X != nil && action.Event.Y != nil {
		return &Point{X: *action.Event.X, Y: *action.Event.Y}
	}
	rect := targetRect(action)
	if rect == nil {
		return nil
	}
	p := rectCenter(*rect)
	return &p
}

// relativeSize is how large the target is relative to the viewport, on its dominant axis.
func relativeSize(r Rect, viewport Viewport) float64 {
	return math.Max(r.Width/viewport.Width, r.Height/viewport.Height)
}

func unionRect(rects []Rect) Rect {
	left, top := rects[0].X, rects[0].Y
	right, bottom := rects[0].X+rects[0].Width, rects[0].Y+rects[0].Height
	for _, r := range rects[1:] {
		left = math.Min(left, r.X)
		top = math.Min(top, r.Y)
		right = math.Max(right, r.X+r.Width)
		bottom = math.Max(bottom, r.Y+r.Height)
	}
	return Rect{X: left, Y: top, Width: right - left, Height: bottom - top}
}

// Clamp constrains value to the range [minVal, maxVal].
func Clamp(value, minVal, maxVal float64) float64 {
	return math.Min(maxVal, math.Max(minVal, value))
}
