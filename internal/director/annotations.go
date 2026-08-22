package director

import (
	"regexp"
	"strings"
)

// Deterministic, deliberately terse captions (spec 21). No sentences, no theme system,
// no manual editor — just the target's own label, or the shortcut the user pressed.
// Direct port of src/director/annotations.js.

// Annotation is a timed caption shown during playback.
type Annotation struct {
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
	Kind    string  `json:"kind"`
	Text    string  `json:"text"`
}

var annotationWhitespace = regexp.MustCompile(`\s+`)

func truncateText(text string, maxLen int) string {
	clean := strings.TrimSpace(annotationWhitespace.ReplaceAllString(text, " "))
	runes := []rune(clean)
	if len(runes) > maxLen {
		return string(runes[:maxLen-1]) + "…"
	}
	return clean
}

func planAnnotations(actions []Action, timeline []Segment, viewport Viewport, config Config) []Annotation {
	cfg := config.Annotations
	var planned []Annotation

	for _, action := range actions {
		tMs := sourceTimeToOutputTime(action.StartMs, timeline)

		if action.Kind == "shortcut" {
			planned = append(planned, Annotation{
				StartMs: tMs, EndMs: tMs + cfg.ShortcutDurationMs, Kind: "shortcut", Text: action.Event.Key,
			})
			continue
		}

		if action.Kind != "click" && action.Kind != "input" {
			continue
		}
		if action.Event.Target == nil || action.Event.Target.Label == "" {
			continue
		}
		label := action.Event.Target.Label

		// Large obvious controls are explained well enough by the cursor and the zoom.
		rect := targetRect(action)
		if rect == nil || relativeSize(*rect, viewport) > cfg.MaxRelSizeForLabel {
			continue
		}

		text := truncateText(label, cfg.MaxTextLength)
		if text == "" {
			continue
		}

		// Typing produces a run of input events on one field; one caption is enough.
		if n := len(planned); n > 0 {
			prev := &planned[n-1]
			if prev.Text == text && tMs <= prev.EndMs {
				prev.EndMs = tMs + cfg.LabelDurationMs
				continue
			}
		}

		planned = append(planned, Annotation{StartMs: tMs, EndMs: tMs + cfg.LabelDurationMs, Kind: "label", Text: text})
	}

	// Never show two captions at once: an earlier one simply ends when the next begins.
	for i := 0; i < len(planned)-1; i++ {
		limit := planned[i+1].StartMs - cfg.MinGapMs
		if planned[i].EndMs > limit {
			planned[i].EndMs = limit
		}
	}

	out := make([]Annotation, 0, len(planned))
	for _, a := range planned {
		if a.EndMs > a.StartMs {
			out = append(out, a)
		}
	}
	return out
}
