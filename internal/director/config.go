package director

// Every post-production constant lives here (spec 15.3), mirroring
// src/director/config.js's defaultConfig exactly. The Node CLI's overrides mechanism has no
// caller yet (pipeline.js always passes {}), so it is not ported — YAGNI until something
// needs it.

// PauseConfig configures pause detection and gap classification.
type PauseConfig struct {
	NaturalPauseMs     float64
	HesitationMs       float64
	KeepAfterActionMs  float64
	KeepBeforeActionMs float64
	HeadKeepMs         float64
	TailKeepMs         float64
}

// NetworkConfig configures network request visibility.
type NetworkConfig struct {
	RelevantTypes   []string
	MaxRequestMs    float64
	MergeGapMs      float64
	MinOverlapMs    float64
	MinOverlapRatio float64
	KeepHeadMs      float64
	KeepTailMs      float64
	TargetMiddleMs  float64
	MinSpeed        float64
	MaxSpeed        float64
}

// TypingConfig configures how a typing run — a contiguous same-field run of keystrokes,
// grouped by computePairableUnits — plays back. It is real footage, not synthetic: Speed
// simply compresses the dead time between keystrokes uniformly across the whole run, the same
// way network-wait/hesitation compression already does elsewhere, instead of collapsing each
// inter-keystroke gap to its own beat/hold.
type TypingConfig struct {
	Speed float64
}

// CursorConfig configures the cursor overlay.
type CursorConfig struct {
	MinMoveMs    float64
	MaxMoveMs    float64
	PxPerMs      float64
	SettleMs     float64
	ClickPulseMs float64
	SizePx       float64
}

// CameraStep defines a zoom level threshold.
type CameraStep struct {
	MinRelSize float64
	Zoom       float64
}

// CameraConfig configures the virtual camera.
type CameraConfig struct {
	MaxZoom            float64
	Steps              []CameraStep
	TextZoomRange      [2]float64
	TextRoles          []string
	PaddingPx          float64
	GroupMaxGapMs      float64
	GroupRadiusRatio   float64
	LeadMs             float64
	TrailMs            float64
	TransitionMs       float64
	MergeGapMs         float64
	MinInterestingZoom float64
}

// AnnotationsConfig configures on-screen captions.
type AnnotationsConfig struct {
	MaxRelSizeForLabel float64
	MaxTextLength      int
	LabelDurationMs    float64
	ShortcutDurationMs float64
	MinGapMs           float64
}

// AudioConfig configures audio mixing.
type AudioConfig struct {
	// TypingMinGapMs prevents rapid input events from turning into a distracting
	// stream of key sounds after the timeline is compressed.
	TypingMinGapMs float64
}

// VoiceConfig tunes the sequential cue-placement pass in voice.go (docs/plans/
// 2026-08-19-voice-annotations.md Task 5). Inert when a session carries no voice cues at
// all — every field here is only ever read from planVoice's own code path.
type VoiceConfig struct {
	// DefaultHoldMs is the floor for a cue's computed lead-in (naturalHold), before the
	// gap-length clamp is applied. See the plan's "Sequential placement pass" pseudocode.
	DefaultHoldMs float64
}

// Config aggregates all post-production configuration.
type Config struct {
	Pause       PauseConfig
	Network     NetworkConfig
	Typing      TypingConfig
	Cursor      CursorConfig
	Camera      CameraConfig
	Annotations AnnotationsConfig
	Audio       AudioConfig
	Voice       VoiceConfig
}

// DefaultConfig returns the standard configuration.
func DefaultConfig() Config {
	return Config{
		Pause: PauseConfig{
			NaturalPauseMs:     800,
			HesitationMs:       1200,
			KeepAfterActionMs:  500,
			KeepBeforeActionMs: 250,
			HeadKeepMs:         600,
			TailKeepMs:         800,
		},
		Network: NetworkConfig{
			RelevantTypes:   []string{"xmlhttprequest", "main_frame"},
			MaxRequestMs:    30000,
			MergeGapMs:      150,
			MinOverlapMs:    600,
			MinOverlapRatio: 0.5,
			KeepHeadMs:      400,
			KeepTailMs:      600,
			TargetMiddleMs:  500,
			MinSpeed:        4,
			MaxSpeed:        12,
		},
		Typing: TypingConfig{
			Speed: 4,
		},
		Cursor: CursorConfig{
			MinMoveMs:    180,
			MaxMoveMs:    550,
			PxPerMs:      2.2,
			SettleMs:     60,
			ClickPulseMs: 420,
			SizePx:       22,
		},
		Camera: CameraConfig{
			MaxZoom: 1.4,
			Steps: []CameraStep{
				{MinRelSize: 0.5, Zoom: 1.0},
				{MinRelSize: 0.25, Zoom: 1.15},
				{MinRelSize: 0.12, Zoom: 1.25},
				{MinRelSize: 0, Zoom: 1.35},
			},
			TextZoomRange:      [2]float64{1.15, 1.25},
			TextRoles:          []string{"textbox", "searchbox", "combobox"},
			PaddingPx:          80,
			GroupMaxGapMs:      2500,
			GroupRadiusRatio:   0.38,
			LeadMs:             420,
			TrailMs:            700,
			TransitionMs:       420,
			MergeGapMs:         1200,
			MinInterestingZoom: 1.08,
		},
		Annotations: AnnotationsConfig{
			MaxRelSizeForLabel: 0.25,
			MaxTextLength:      40,
			LabelDurationMs:    1600,
			ShortcutDurationMs: 1600,
			MinGapMs:           150,
		},
		Audio: AudioConfig{
			TypingMinGapMs: 65,
		},
		Voice: VoiceConfig{
			DefaultHoldMs: 0,
		},
	}
}
