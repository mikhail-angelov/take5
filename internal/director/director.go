package director

import "math"

// Direct port of src/director/director.js. Pure: same session.json in, same project.json
// out. No LLM, no I/O.

// ProjectVersion is the schema version of project.json.
// Version 3: CursorPlan includes ClickPulseMs and SizePx (render params moved from Config).
const ProjectVersion = 3

// TimelineSegment is an interval in the output timeline with a specific playback speed. A
// held (freeze-frame) segment instead carries HoldMs > 0: SourceStartMs == SourceEndMs, Speed
// is meaningless, and the renderer holds that source instant for HoldMs of output time.
type TimelineSegment struct {
	SourceStartMs int64   `json:"sourceStartMs"`
	SourceEndMs   int64   `json:"sourceEndMs"`
	Speed         float64 `json:"speed"`
	HoldMs        int64   `json:"holdMs,omitempty"`
}

// DebugGap describes why a gap was eliminated during pause classification.
type DebugGap struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Reason  string `json:"reason"`
}

// Debug holds diagnostic information about the project for the analyze subcommand.
type Debug struct {
	ActionCount          int        `json:"actionCount"`
	NetworkBusyIntervals []Interval `json:"networkBusyIntervals"`
	Gaps                 []DebugGap `json:"gaps"`
}

// AudioCue is a synthetic sound effect placed on the final, edited timeline.
// Its time is deliberately output time: the renderer must never repeat the
// source-time arithmetic while mixing audio.
type AudioCue struct {
	Kind string `json:"kind"`
	TMs  int64  `json:"tMs"`
}

// Project is the output of the director stage: the plan for rendering.
type Project struct {
	Version          int               `json:"version"`
	Source           string            `json:"source"`
	SessionID        string            `json:"sessionId"`
	Viewport         Viewport          `json:"viewport"`
	DurationMs       int64             `json:"durationMs"`
	SourceDurationMs int64             `json:"sourceDurationMs"`
	Timeline         []TimelineSegment `json:"timeline"`
	Cursor           CursorPlan        `json:"cursor"`
	Camera           []CameraKeyframe  `json:"camera"`
	Annotations      []Annotation      `json:"annotations"`
	Audio            []AudioCue        `json:"audio"`
	Voice            []PlacedVoiceCue  `json:"voice"`
	// Not consumed by the renderer; makes `analyze` output explainable.
	Debug Debug `json:"debug"`
}

func roundTo(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

// Direct transforms a recorded session into a render plan.
func Direct(session Session) Project {
	config := DefaultConfig()
	viewport := session.Viewport

	actions := meaningfulActions(session.Events)
	busy := networkBusyIntervals(session.Network, config.Network)

	// computePairableUnits is the shared source of truth for both timeline segmentation
	// (planSegments) and voice-cue pairing (pairVoiceCues) — computed once here and passed to
	// both, so there is no ordering dependency between them. planVoicePlacement runs after
	// planSegments/buildTimeline, once sourceTimeToOutputTime exists — see voice.go's own doc
	// comment. It now owns timeline construction too (it may extend plan.Segments with freeze
	// inserts), so everything downstream (cursor, camera, annotations, audio) takes the
	// timeline it returns rather than building one separately.
	pairableUnits := computePairableUnits(actions)
	voicePairings := pairVoiceCues(keptVoiceCues(session.VoiceCues), pairableUnits)

	plan := planSegments(pairableUnits, session.DurationMs, busy, config)

	timeline, voice := planVoicePlacement(voicePairings, pairableUnits, plan.Segments, config.Voice)
	cursor := planCursor(actions, timeline, config)
	camera := planCamera(actions, timeline, viewport, config)
	annotations := planAnnotations(actions, timeline, viewport, config)
	audio := planAudio(actions, timeline, config)

	tlOut := make([]TimelineSegment, len(timeline))
	for i, s := range timeline {
		tlOut[i] = TimelineSegment{
			SourceStartMs: int64(math.Round(s.SourceStartMs)),
			SourceEndMs:   int64(math.Round(s.SourceEndMs)),
			Speed:         roundTo(s.Speed, 4),
			HoldMs:        int64(math.Round(s.HoldMs)),
		}
	}

	debugGaps := make([]DebugGap, len(plan.Gaps))
	for i, g := range plan.Gaps {
		debugGaps[i] = DebugGap{
			StartMs: int64(math.Round(g.StartMs)),
			EndMs:   int64(math.Round(g.EndMs)),
			Reason:  g.Reason,
		}
	}

	networkBusy := busy
	if networkBusy == nil {
		networkBusy = []Interval{}
	}

	return Project{
		Version:          ProjectVersion,
		Source:           "raw.mp4",
		SessionID:        session.SessionID,
		Viewport:         viewport,
		DurationMs:       int64(math.Round(outputDurationMs(timeline))),
		SourceDurationMs: int64(math.Round(sourceDurationMs(timeline))),
		Timeline:         tlOut,
		Cursor:           cursor,
		Camera:           camera,
		Annotations:      annotations,
		Audio:            audio,
		Voice:            voice,
		Debug: Debug{
			ActionCount:          len(actions),
			NetworkBusyIntervals: networkBusy,
			Gaps:                 debugGaps,
		},
	}
}
