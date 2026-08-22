package director

import (
	"math"
	"slices"
)

// Plans the virtual camera (spec 20). The raw video is never zoomed; this only decides
// where a viewer should be looking and how much of the frame that needs.
// Direct port of src/director/camera.js.

// CameraKeyframe is a point in time with a camera state (zoom and pan).
type CameraKeyframe struct {
	TMs  float64 `json:"tMs"`
	Zoom float64 `json:"zoom"`
	Cx   float64 `json:"cx"`
	Cy   float64 `json:"cy"`
}

// zoomForTarget: small targets get more zoom; large obvious ones get none (spec 20.2).
func zoomForTarget(rect Rect, role string, viewport Viewport, cfg CameraConfig) float64 {
	relSize := relativeSize(rect, viewport)
	zoom := 1.0
	for _, step := range cfg.Steps {
		if relSize >= step.MinRelSize {
			zoom = step.Zoom
			break
		}
	}
	if slices.Contains(cfg.TextRoles, role) {
		zoom = Clamp(zoom, cfg.TextZoomRange[0], cfg.TextZoomRange[1])
	}
	return math.Min(zoom, cfg.MaxZoom)
}

type cameraTarget struct {
	TMs    float64
	Rect   Rect
	Center Point
	Zoom   float64
}

func groupCameraTargets(targets []cameraTarget, viewport Viewport, cfg CameraConfig) [][]cameraTarget {
	radius := math.Hypot(viewport.Width, viewport.Height) * cfg.GroupRadiusRatio
	var groups [][]cameraTarget

	for _, target := range targets {
		if n := len(groups); n > 0 {
			group := groups[n-1]
			last := group[len(group)-1]
			nearInTime := target.TMs-last.TMs <= cfg.GroupMaxGapMs
			nearInSpace := math.Hypot(target.Center.X-last.Center.X, target.Center.Y-last.Center.Y) <= radius
			if nearInTime && nearInSpace {
				groups[n-1] = append(group, target)
				continue
			}
		}
		groups = append(groups, []cameraTarget{target})
	}
	return groups
}

type shotRange struct {
	Group          []cameraTarget
	StartMs, EndMs float64
}

type shot struct {
	Zoom           float64
	Cx, Cy         float64
	StartMs, EndMs float64
}

func shotGeometry(r shotRange, allTargets []cameraTarget, viewport Viewport, cfg CameraConfig) shot {
	// Whatever else the user does while this shot is on screen must still be inside the
	// frame, even if that target was not worth zooming for on its own.
	var inShot []cameraTarget
	for _, t := range allTargets {
		if t.TMs >= r.StartMs && t.TMs <= r.EndMs {
			inShot = append(inShot, t)
		}
	}
	source := inShot
	if len(source) == 0 {
		source = r.Group
	}
	rects := make([]Rect, len(source))
	for i, t := range source {
		rects[i] = t.Rect
	}
	box := unionRect(rects)
	paddedWidth := box.Width + cfg.PaddingPx*2
	paddedHeight := box.Height + cfg.PaddingPx*2

	// Never zoom past what fits everything on screen, nor past what the group asked for.
	fitZoom := math.Min(viewport.Width/paddedWidth, viewport.Height/paddedHeight)
	wantedZoom := r.Group[0].Zoom
	for _, t := range r.Group[1:] {
		wantedZoom = math.Max(wantedZoom, t.Zoom)
	}
	zoom := Clamp(math.Min(fitZoom, wantedZoom), 1, cfg.MaxZoom)

	center := rectCenter(box)
	halfWidth := viewport.Width / (2 * zoom)
	halfHeight := viewport.Height / (2 * zoom)

	return shot{
		Zoom:    zoom,
		Cx:      Clamp(center.X, halfWidth, viewport.Width-halfWidth),
		Cy:      Clamp(center.Y, halfHeight, viewport.Height-halfHeight),
		StartMs: r.StartMs,
		EndMs:   r.EndMs,
	}
}

func planCamera(actions []Action, timeline []Segment, viewport Viewport, config Config) []CameraKeyframe {
	cfg := config.Camera

	var targets []cameraTarget
	for _, action := range actions {
		rect := targetRect(action)
		if rect == nil {
			continue
		}
		role := action.Event.Target.Role
		targets = append(targets, cameraTarget{
			TMs:    sourceTimeToOutputTime(action.StartMs, timeline),
			Rect:   *rect,
			Center: rectCenter(*rect),
			Zoom:   zoomForTarget(*rect, role, viewport, cfg),
		})
	}
	if len(targets) == 0 {
		return []CameraKeyframe{}
	}

	// Only targets small enough to be worth zooming drive a shot; a big obvious button
	// never pulls the camera in, it just has to stay in frame if one is already running.
	var anchors []cameraTarget
	for _, t := range targets {
		if t.Zoom > 1 {
			anchors = append(anchors, t)
		}
	}
	if len(anchors) == 0 {
		return []CameraKeyframe{}
	}

	groups := groupCameraTargets(anchors, viewport, cfg)
	ranges := make([]shotRange, len(groups))
	for i, g := range groups {
		ranges[i] = shotRange{
			Group:   g,
			StartMs: g[0].TMs - cfg.LeadMs,
			EndMs:   g[len(g)-1].TMs + cfg.TrailMs,
		}
	}

	// Windows are resolved before geometry: a shot cut short by the next one must not be
	// forced to frame targets it no longer covers.
	for i := 1; i < len(ranges); i++ {
		ranges[i].StartMs = math.Max(ranges[i].StartMs, ranges[i-1].StartMs)
		ranges[i-1].EndMs = math.Min(ranges[i-1].EndMs, ranges[i].StartMs)
	}

	var shots []shot
	for _, r := range ranges {
		if r.EndMs <= r.StartMs {
			continue
		}
		s := shotGeometry(r, targets, viewport, cfg)
		if s.Zoom >= cfg.MinInterestingZoom {
			shots = append(shots, s)
		}
	}
	if len(shots) == 0 {
		return []CameraKeyframe{}
	}

	neutralCx := viewport.Width / 2
	neutralCy := viewport.Height / 2

	var keyframes []CameraKeyframe
	chainOpen := false

	for i := range shots {
		s := shots[i]
		var prev *shot
		if i > 0 {
			prev = &shots[i-1]
		}
		// Shots close together stay zoomed and pan instead, so the output never flickers
		// in and out (spec 20.3).
		continues := chainOpen && prev != nil && s.StartMs-prev.EndMs < cfg.MergeGapMs

		if !continues {
			openAt := math.Max(0, s.StartMs-cfg.TransitionMs)
			keyframes = append(keyframes, CameraKeyframe{TMs: openAt, Zoom: 1, Cx: neutralCx, Cy: neutralCy})
		}
		keyframes = append(keyframes,
			CameraKeyframe{TMs: s.StartMs, Zoom: s.Zoom, Cx: s.Cx, Cy: s.Cy},
			CameraKeyframe{TMs: s.EndMs, Zoom: s.Zoom, Cx: s.Cx, Cy: s.Cy},
		)

		var next *shot
		if i+1 < len(shots) {
			next = &shots[i+1]
		}
		willContinue := next != nil && next.StartMs-s.EndMs < cfg.MergeGapMs
		if !willContinue {
			keyframes = append(keyframes, CameraKeyframe{TMs: s.EndMs + cfg.TransitionMs, Zoom: 1, Cx: neutralCx, Cy: neutralCy})
			chainOpen = false
		} else {
			chainOpen = true
		}
	}

	// Guard against duplicate or backwards timestamps from degenerate shots.
	filtered := make([]CameraKeyframe, 0, len(keyframes))
	for i, kf := range keyframes {
		if i == 0 || kf.TMs > keyframes[i-1].TMs {
			filtered = append(filtered, kf)
		}
	}
	return filtered
}
