package director

import "math"

// planAudio keeps sound effects in the same final-time coordinate system as
// cursor, camera and annotations. A click is one cue; input events get a small
// debounce after editing so fast typing stays pleasant rather than noisy.
func planAudio(actions []Action, timeline []Segment, config Config) []AudioCue {
	cues := make([]AudioCue, 0, len(actions))
	lastTypingMs := math.Inf(-1)

	for _, action := range actions {
		if action.Kind != "click" && action.Kind != "input" {
			continue
		}
		if !isSourceTimeKept(action.StartMs, timeline) {
			continue
		}

		outputMs := sourceTimeToOutputTime(action.StartMs, timeline)
		if action.Kind == "input" {
			if outputMs-lastTypingMs < config.Audio.TypingMinGapMs {
				continue
			}
			lastTypingMs = outputMs
		}

		cues = append(cues, AudioCue{
			Kind: action.Kind,
			TMs:  int64(math.Round(outputMs)),
		})
	}
	return cues
}
