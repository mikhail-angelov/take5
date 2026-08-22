package render

import (
	"strings"
	"testing"

	"take5/internal/director"
)

func TestBuildAudioFilterGraphPlacesCuesAndLimitsOutputToTheVideo(t *testing.T) {
	graph := BuildAudioFilterGraph([]director.AudioCue{
		{Kind: "click", TMs: 125},
		{Kind: "input", TMs: 350},
	}, nil, 1000)
	want := "[1:a]volume=0.06,atrim=duration=1.000000,afade=t=in:st=0:d=0.250000,afade=t=out:st=0.750000:d=0.250000[music];\n" +
		"[2:a]volume=0.16,adelay=125:all=1[cue0];\n" +
		"[3:a]volume=0.1,adelay=350:all=1[cue1];\n" +
		"[music][cue0][cue1]amix=inputs=3:duration=first:normalize=0,alimiter=limit=0.95[audio]"
	if graph != want {
		t.Errorf("filter graph =\n%s\nwant\n%s", graph, want)
	}
}

// TestBuildAudioFilterGraphIsByteIdenticalWithNoVoiceCues is the concrete proof the feature is
// inert when off, for the audio path specifically (docs/plans/2026-08-19-voice-annotations.md
// Task 6 — the corpus golden suite in golden_test.go never exercises BuildAudioFilterGraph, so
// this is the only place that proof exists). An empty, non-nil voiceCues slice must produce
// byte-identical output to a nil one and to the pre-voice call shape above.
func TestBuildAudioFilterGraphIsByteIdenticalWithNoVoiceCues(t *testing.T) {
	cues := []director.AudioCue{{Kind: "click", TMs: 125}, {Kind: "input", TMs: 350}}
	withNil := BuildAudioFilterGraph(cues, nil, 1000)
	withEmpty := BuildAudioFilterGraph(cues, []director.PlacedVoiceCue{}, 1000)
	if withNil != withEmpty {
		t.Errorf("nil voiceCues produced a different graph than an empty slice:\nnil:   %s\nempty: %s", withNil, withEmpty)
	}
	if strings.Contains(withNil, "voice") {
		t.Errorf("graph with no voice cues mentions voice at all: %s", withNil)
	}
}

// TestBuildAudioFilterGraphMixesVoiceCues covers more than one placement shape per this
// task's own testing note: a cue with a lead-in hold, a cue paired to a typing run (both
// placements look identical at this layer — only tMs/duration/file matter here, pairing
// shape is director's concern), and back-to-back cues near the barrier clamp (consecutive
// tMs values with no gap between them).
func TestBuildAudioFilterGraphMixesVoiceCues(t *testing.T) {
	tests := []struct {
		name  string
		cues  []director.PlacedVoiceCue
		wants []string
	}{
		{
			name: "single cue with a lead-in hold",
			cues: []director.PlacedVoiceCue{{TMs: 800, AudioFile: "voice/cue-0.mp3", DurationMs: 1800}},
			wants: []string{
				"[4:a]volume=1,adelay=800:all=1[voice0]",
				"[music][voice0]amix=inputs=2",
			},
		},
		{
			name: "cue paired to a typing run",
			cues: []director.PlacedVoiceCue{{TMs: 2000, AudioFile: "voice/cue-1.mp3", DurationMs: 2400}},
			wants: []string{
				"[4:a]volume=1,adelay=2000:all=1[voice0]",
			},
		},
		{
			name: "back-to-back cues at the barrier clamp",
			cues: []director.PlacedVoiceCue{
				{TMs: 1000, AudioFile: "voice/cue-0.mp3", DurationMs: 900},
				{TMs: 1000, AudioFile: "voice/cue-1.mp3", DurationMs: 500}, // clamped to the same tMs as cue-0's action end
			},
			wants: []string{
				"[4:a]volume=1,adelay=1000:all=1[voice0]",
				"[5:a]volume=1,adelay=1000:all=1[voice1]",
				"[music][voice0][voice1]amix=inputs=3",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := BuildAudioFilterGraph(nil, tt.cues, 5000)
			for _, want := range tt.wants {
				if !strings.Contains(graph, want) {
					t.Errorf("graph = %s\nwant it to contain %q", graph, want)
				}
			}
		})
	}
}

func TestBuildAudioFilterGraphVoiceInputIndexAccountsForClickTypingCues(t *testing.T) {
	// Voice input indices are fixed by VisualArgs' -i ordering (source, music, click,
	// typing, then voice clips) — they must not shift just because click/typing sound-effect
	// cues are also present, since those reuse fixed inputs 2/3 rather than adding new ones.
	graph := BuildAudioFilterGraph(
		[]director.AudioCue{{Kind: "click", TMs: 100}, {Kind: "input", TMs: 200}},
		[]director.PlacedVoiceCue{{TMs: 300, AudioFile: "voice/cue-0.mp3", DurationMs: 400}},
		5000,
	)
	if !strings.Contains(graph, "[4:a]volume=1,adelay=300:all=1[voice0]") {
		t.Errorf("graph = %s, want voice0 to read from input 4 regardless of click/typing cue count", graph)
	}
}

func TestVisualArgsMapsAudioAndVideo(t *testing.T) {
	args := VisualArgs("clean.mp4", "pass-b.filter", "demo.mp4", 30, defaultCRF, defaultPreset, defaultPixelFormat, audioAssets{
		Music: "audio/background.mp3", Click: "audio/click.wav", Typing: "audio/typing.wav",
	}, nil)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-stream_loop -1 -i audio/background.mp3",
		"-map [out] -map [audio]",
		"-c:a aac",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args = %q, want %q", joined, want)
		}
	}
}

func TestVisualArgsAppendsVoiceClipsAfterTyping(t *testing.T) {
	args := VisualArgs("clean.mp4", "pass-b.filter", "demo.mp4", 30, defaultCRF, defaultPreset, defaultPixelFormat, audioAssets{
		Music: "audio/background.mp3", Click: "audio/click.wav", Typing: "audio/typing.wav",
	}, []string{"voice/cue-0.mp3", "voice/cue-1.mp3"})
	joined := strings.Join(args, " ")
	want := "-i audio/click.wav -i audio/typing.wav -i voice/cue-0.mp3 -i voice/cue-1.mp3 -filter_complex_script"
	if !strings.Contains(joined, want) {
		t.Errorf("args = %q, want it to contain %q", joined, want)
	}
}
