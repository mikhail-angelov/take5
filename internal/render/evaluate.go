package render

import "take5/internal/director"

// Samples a Project's camera and cursor plans at an output instant. Director produces the
// plan (keyframes, moves, holds); evaluating that plan at a specific tMs for the overlay is
// render's own concern, not Director policy, so these functions live here rather than being
// called back into internal/director.

// clamp constrains value to the range [minVal, maxVal].
func clamp(value, minVal, maxVal float64) float64 {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

// easeInOutCubic is the only easing the MVP needs (spec 19.3).
func easeInOutCubic(p float64) float64 {
	if p < 0.5 {
		return 4 * p * p * p
	}
	x := -2*p + 2
	return 1 - (x*x*x)/2
}

// cameraStateAt interpolates a camera state at a specific time.
func cameraStateAt(keyframes []director.CameraKeyframe, tMs float64, viewport director.Viewport) director.CameraKeyframe {
	neutral := director.CameraKeyframe{Zoom: 1, Cx: viewport.Width / 2, Cy: viewport.Height / 2}
	if len(keyframes) == 0 {
		return neutral
	}
	if tMs <= keyframes[0].TMs {
		return keyframes[0]
	}
	last := keyframes[len(keyframes)-1]
	if tMs >= last.TMs {
		return last
	}

	for i := 1; i < len(keyframes); i++ {
		a := keyframes[i-1]
		b := keyframes[i]
		if tMs > b.TMs {
			continue
		}
		p := (tMs - a.TMs) / (b.TMs - a.TMs)
		eased := easeInOutCubic(p)
		return director.CameraKeyframe{
			Zoom: a.Zoom + (b.Zoom-a.Zoom)*eased,
			Cx:   a.Cx + (b.Cx-a.Cx)*eased,
			Cy:   a.Cy + (b.Cy-a.Cy)*eased,
		}
	}
	return neutral
}

// cursorPositionAt resolves the cursor position at an output instant, to sample the overlay.
func cursorPositionAt(plan director.CursorPlan, tMs float64) *director.CursorPoint {
	if plan.Start == nil {
		return nil
	}
	position := *plan.Start
	for _, move := range plan.Moves {
		if tMs <= move.StartMs {
			break
		}
		if tMs >= move.EndMs {
			position = move.To
			continue
		}
		p := (tMs - move.StartMs) / (move.EndMs - move.StartMs)
		eased := p
		if move.Ease != "linear" {
			eased = easeInOutCubic(p)
		}
		position = director.CursorPoint{
			X: move.From.X + (move.To.X-move.From.X)*eased,
			Y: move.From.Y + (move.To.Y-move.From.Y)*eased,
		}
	}
	return &position
}

// cursorHeldAt reports whether the primary pointer button is held at an output instant.
func cursorHeldAt(plan director.CursorPlan, tMs float64) bool {
	for _, hold := range plan.Holds {
		if tMs >= hold.StartMs && tMs < hold.EndMs {
			return true
		}
	}
	return false
}
