// Package director turns a raw session into the complete deterministic post-production
// plan: pure functions, same session.json in, same project.json out (spec 14). Direct port
// of src/director/*.js — see docs/plans/go-port.md, Phase 2.
package director

// Rect is a 2D rectangle with position and size.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Target describes the clickable element or text input that was interacted with.
type Target struct {
	Role  string `json:"role"`
	Rect  *Rect  `json:"rect"`
	Label string `json:"label"`
}

// PathPoint is a point in a drag path with its timestamp.
type PathPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	T float64 `json:"t"`
}

// Event is a recorded user interaction from the session.
type Event struct {
	Kind    string      `json:"kind"`
	T       float64     `json:"t"`
	StartMs *float64    `json:"startMs"`
	EndMs   *float64    `json:"endMs"`
	X       *float64    `json:"x"`
	Y       *float64    `json:"y"`
	Target  *Target     `json:"target"`
	Key     string      `json:"key"`
	Path    []PathPoint `json:"path"`
}

// Viewport describes the dimensions and scaling of the captured browser.
type Viewport struct {
	Width            float64 `json:"width"`
	Height           float64 `json:"height"`
	DevicePixelRatio float64 `json:"devicePixelRatio"`
}

// NetworkRecord describes an HTTP request.
type NetworkRecord struct {
	ID      string  `json:"id"`
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
	Type    string  `json:"type"`
	Status  float64 `json:"status"`
	Failed  bool    `json:"failed"`
}

// VoiceCue mirrors one entry of voice.json (internal/voiceover.VoiceCue's wire shape) — the
// `voice` stage's own output, read as a pure input here exactly like Events/Network (docs/
// plans/2026-08-19-voice-annotations.md's pipeline-placement decision). RewrittenText == nil
// is an explicit drop; Direct() must never place such a cue.
type VoiceCue struct {
	ID            string  `json:"id"`
	SourceStartMs float64 `json:"sourceStartMs"`
	SourceEndMs   float64 `json:"sourceEndMs"`
	OriginalText  string  `json:"originalText"`
	RewrittenText *string `json:"rewrittenText"`
	AudioFile     string  `json:"audioFile"`
	TTSDurationMs float64 `json:"ttsDurationMs"`
}

// Session is session.json, as written by internal/session.Writer or its Node predecessor,
// plus voice.json's cues merged in as one more pure input (VoiceCues is nil for the
// pre-feature, byte-identical path — see Task 7's CLI wiring for where the merge happens).
type Session struct {
	Version          int             `json:"version"`
	SessionID        string          `json:"sessionId"`
	StartedAtEpochMs float64         `json:"startedAtEpochMs"`
	DurationMs       float64         `json:"durationMs"`
	URL              string          `json:"url"`
	Viewport         Viewport        `json:"viewport"`
	Capture          map[string]any  `json:"capture"`
	Events           []Event         `json:"events"`
	Network          []NetworkRecord `json:"network"`
	VoiceCues        []VoiceCue      `json:"voiceCues,omitempty"`
}

// Action is one story-carrying event, resolved to its own start/end regardless of whether
// the underlying event carries startMs/endMs or just t. Port of meaningfulActions' element
// shape in src/director/pause-classifier.js.
type Action struct {
	Kind    string
	StartMs float64
	EndMs   float64
	Event   Event
}

// Point is a 2D coordinate.
type Point struct {
	X float64
	Y float64
}
