package director

import "math"

// Plans the synthetic cursor (spec 19). The raw pointer samples are never replayed: the
// final cursor only travels between meaningful targets, so hesitation and wandering vanish.
// Direct port of src/director/cursor.js.

// CursorPoint is an (x, y) coordinate.
type CursorPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// CursorMove is a smooth motion from one point to another.
type CursorMove struct {
	StartMs float64     `json:"startMs"`
	EndMs   float64     `json:"endMs"`
	From    CursorPoint `json:"from"`
	To      CursorPoint `json:"to"`
	Ease    string      `json:"ease"`
}

// CursorClick is a mouse click at a specific time and location.
type CursorClick struct {
	TMs float64 `json:"tMs"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
}

// CursorHold is the interval during which the primary pointer button is held while
// dragging. The renderer uses it to switch from the arrow to a grabbing cursor.
type CursorHold struct {
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
}

// CursorPlan describes the cursor's path through a demo.
type CursorPlan struct {
	Start        *CursorPoint  `json:"start"`
	Moves        []CursorMove  `json:"moves"`
	Clicks       []CursorClick `json:"clicks"`
	Holds        []CursorHold  `json:"holds"`
	ClickPulseMs float64       `json:"clickPulseMs"`
	SizePx       float64       `json:"sizePx"`
}

type cursorWaypoint struct {
	TMs  float64
	X, Y float64
	Ease string
}

// waypoints are ordered arrival points in output time. A drag contributes its real path,
// because drag geometry is part of what the demo is showing (spec 12.7).
func cursorWaypoints(actions []Action, timeline []Segment) []cursorWaypoint {
	var points []cursorWaypoint
	for _, action := range actions {
		if action.Kind == "drag" && len(action.Event.Path) > 0 {
			path := action.Event.Path
			points = append(points, cursorWaypoint{
				TMs: sourceTimeToOutputTime(path[0].T, timeline), X: path[0].X, Y: path[0].Y, Ease: "cubic",
			})
			for _, p := range path[1:] {
				points = append(points, cursorWaypoint{
					TMs: sourceTimeToOutputTime(p.T, timeline), X: p.X, Y: p.Y, Ease: "linear",
				})
			}
			continue
		}

		point := actionPoint(action)
		if point == nil {
			continue
		}
		points = append(points, cursorWaypoint{
			TMs: sourceTimeToOutputTime(action.StartMs, timeline), X: point.X, Y: point.Y, Ease: "cubic",
		})
	}
	return points
}

func moveDuration(distance float64, cfg CursorConfig) float64 {
	return math.Min(cfg.MaxMoveMs, math.Max(cfg.MinMoveMs, distance/cfg.PxPerMs))
}

func planCursor(actions []Action, timeline []Segment, config Config) CursorPlan {
	cfg := config.Cursor
	points := cursorWaypoints(actions, timeline)
	if len(points) == 0 {
		return CursorPlan{Moves: []CursorMove{}, Clicks: []CursorClick{}, Holds: []CursorHold{}}
	}

	moves := make([]CursorMove, 0)
	previous := points[0]

	for _, point := range points[1:] {
		distance := math.Hypot(point.X-previous.X, point.Y-previous.Y)

		if point.Ease == "linear" {
			// Drag interior: follow the recorded path at its recorded pace.
			if point.TMs > previous.TMs && distance > 0 {
				moves = append(moves, CursorMove{
					StartMs: previous.TMs, EndMs: point.TMs,
					From: CursorPoint{X: previous.X, Y: previous.Y}, To: CursorPoint{X: point.X, Y: point.Y},
					Ease: "linear",
				})
			}
			previous = point
			continue
		}

		if distance >= 1 {
			// Arrive slightly early so the click lands on a settled cursor.
			arriveMs := point.TMs - cfg.SettleMs
			available := arriveMs - previous.TMs
			if available > 0 {
				duration := math.Min(moveDuration(distance, cfg), available)
				moves = append(moves, CursorMove{
					StartMs: arriveMs - duration, EndMs: arriveMs,
					From: CursorPoint{X: previous.X, Y: previous.Y}, To: CursorPoint{X: point.X, Y: point.Y},
					Ease: "cubic",
				})
			}
		}
		previous = point
	}

	clicks := make([]CursorClick, 0)
	holds := make([]CursorHold, 0)
	for _, action := range actions {
		switch action.Kind {
		case "click":
			point := actionPoint(action)
			if point == nil {
				continue
			}
			clicks = append(clicks, CursorClick{TMs: sourceTimeToOutputTime(action.StartMs, timeline), X: point.X, Y: point.Y})
		case "drag":
			startMs := sourceTimeToOutputTime(action.StartMs, timeline)
			endMs := sourceTimeToOutputTime(action.EndMs, timeline)
			if endMs > startMs {
				holds = append(holds, CursorHold{StartMs: startMs, EndMs: endMs})
			}
		}
	}

	start := CursorPoint{X: points[0].X, Y: points[0].Y}
	return CursorPlan{
		Start:        &start,
		Moves:        moves,
		Clicks:       clicks,
		Holds:        holds,
		ClickPulseMs: cfg.ClickPulseMs,
		SizePx:       cfg.SizePx,
	}
}
