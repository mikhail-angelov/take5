package render

import (
	"fmt"
	"math"
	"strings"

	"take5/internal/director"
)

const (
	musicVolume  = 0.06
	clickVolume  = 0.16
	typingVolume = 0.10
	// voiceVolume is deliberately the highest gain of the group (docs/plans/
	// 2026-08-19-voice-annotations.md Task 6): it is the narration, everything else here is
	// ambience or a sound effect meant to sit under it.
	voiceVolume = 1.0
	fadeMs      = int64(250)
)

// voiceInputStart is the first ffmpeg input index a voice clip occupies. Inputs 0-3 are
// fixed (source video, music, click, typing — see VisualArgs); voice clips are per-session
// files, not embedded assets, so each project.Voice entry becomes its own -i argument
// appended after typing, in order, and referenced here as [4:a], [5:a], ...
const voiceInputStart = 4

// BuildAudioFilterGraph combines a quiet looped music bed, the synthetic cues planned by the
// Director, and — when present — placed voice narration clips. Input 1 is music, 2 is click,
// 3 is typing, 4+ are voice clips in project.Voice order; input 0 remains the temporally
// edited video. Byte-identical to the pre-voice output when voiceCues is empty (verified in
// TestBuildAudioFilterGraphIsByteIdenticalWithNoVoiceCues).
func BuildAudioFilterGraph(cues []director.AudioCue, voiceCues []director.PlacedVoiceCue, durationMs int64) string {
	duration := seconds(durationMs)
	fadeOutStartMs := math.Max(0, float64(durationMs-fadeMs))
	lines := []string{fmt.Sprintf(
		"[1:a]volume=%g,atrim=duration=%s,afade=t=in:st=0:d=%s,afade=t=out:st=%s:d=%s[music]",
		musicVolume, duration, seconds(fadeMs), seconds(int64(fadeOutStartMs)), seconds(fadeMs),
	)}

	inputs := make([]string, 1, 1+len(cues)+len(voiceCues))
	inputs[0] = "[music]"
	for i, cue := range cues {
		input := "[2:a]"
		volume := clickVolume
		if cue.Kind == "input" {
			input = "[3:a]"
			volume = typingVolume
		}
		label := fmt.Sprintf("[cue%d]", i)
		lines = append(lines, fmt.Sprintf(
			"%svolume=%g,adelay=%d:all=1%s", input, volume, cue.TMs, label,
		))
		inputs = append(inputs, label)
	}

	for i, vc := range voiceCues {
		label := fmt.Sprintf("[voice%d]", i)
		lines = append(lines, fmt.Sprintf(
			"[%d:a]volume=%g,adelay=%d:all=1%s", voiceInputStart+i, voiceVolume, vc.TMs, label,
		))
		inputs = append(inputs, label)
	}

	if len(inputs) == 1 {
		lines = append(lines, "[music]anull[audio]")
	} else {
		lines = append(lines, fmt.Sprintf(
			"%samix=inputs=%d:duration=first:normalize=0,alimiter=limit=0.95[audio]",
			strings.Join(inputs, ""), len(inputs),
		))
	}
	return strings.Join(lines, ";\n")
}
