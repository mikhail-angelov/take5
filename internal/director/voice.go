package director

import (
	"math"
	"sort"
)

// planVoice implements Task 5 of docs/plans/completed/2026-08-19-voice-annotations.md (cue
// pairing) and docs/plans/completed/2026-08-21-voice-sync-and-freeze-frame.md (sequential
// ceiling/floor placement with freeze-frame insertion). pairableUnit/computePairableUnits
// (pause_classifier.go) is the shared source of truth for both this file's cue pairing and
// planSegments' own timeline segmentation — computed once in Direct() and passed to both, so
// there is no dependency between them any more (docs/plans/20260822-action-model-and-robust-
// pass-a.md). planVoicePlacement runs after planSegments/buildTimeline, and OWNS timeline
// construction from that point on rather than merely consuming one: it needs
// sourceTimeToOutputTime, which only exists once a timeline exists, but a freeze inserted for
// one cue changes the timeline every later cue's own placement depends on — so it builds the
// initial timeline itself, then rebuilds it in place immediately after each insert, before
// moving to the next pairing. This mirrors buildTimeline's own sequential, single-pass style
// rather than introducing a second timing model alongside it.

// voicePairing is a cue paired to the action it explains. Unit is nil for a trailing cue —
// narration with nothing left to explain (e.g. a wrap-up after the last recorded action) —
// which is placed at the end of the video instead of dropped; see planVoicePlacement.
type voicePairing struct {
	Cue  VoiceCue
	Unit *pairableUnit
}

// keptVoiceCues returns only the cues the LLM did not drop, sorted by source time — dropped
// cues (RewrittenText == nil) must never be paired or placed (Task 10 acceptance criterion).
func keptVoiceCues(cues []VoiceCue) []VoiceCue {
	kept := make([]VoiceCue, 0, len(cues))
	for _, c := range cues {
		if c.RewrittenText != nil {
			kept = append(kept, c)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].SourceStartMs < kept[j].SourceStartMs })
	return kept
}

// pairVoiceCues implements the locked design decision: cue<->action pairing is strictly 1:1,
// and a cue always explains the action that follows it in time. A two-pointer walk over
// cues and units (both already time-ordered) gives this directly: each cue claims the
// earliest not-yet-claimed unit that starts at or after the cue's own start. A cue with no
// remaining unit to pair to (narration after the last action — a wrap-up, say) has nothing
// left to explain, but is not dropped: it comes back with Unit == nil, a trailing cue
// planVoicePlacement places at the end of the video instead. Once units run out, every
// later cue is trailing too — cues and units are both processed in time order, so a cursor
// that has run out of units can never find one for a still-later cue either.
func pairVoiceCues(cues []VoiceCue, units []pairableUnit) []voicePairing {
	var pairings []voicePairing
	unitIdx := 0
	for _, cue := range cues {
		for unitIdx < len(units) && units[unitIdx].StartMs < cue.SourceStartMs {
			unitIdx++
		}
		if unitIdx >= len(units) {
			pairings = append(pairings, voicePairing{Cue: cue, Unit: nil})
			continue
		}
		unit := units[unitIdx]
		pairings = append(pairings, voicePairing{Cue: cue, Unit: &unit})
		unitIdx++
	}
	return pairings
}

// PlacedVoiceCue is one voice cue's placement on the edited timeline — output-time start,
// the audio clip to mix, and its duration. Same shape discipline as AudioCue: output time
// only, so the renderer never repeats the source-time arithmetic while mixing.
type PlacedVoiceCue struct {
	TMs        int64  `json:"tMs"`
	AudioFile  string `json:"audioFile"`
	DurationMs int64  `json:"durationMs"`
}

// planVoicePlacement is the sequential ceiling/floor placement pass. For each pairing, in
// order:
//
//   - the CEILING is normally the output-time start of the very next action **overall** —
//     whether or not that action has a cue of its own. Talking over the cue's OWN annotated
//     action is fine and expected: real narration often keeps going while the action it
//     describes happens, so there is no ceiling from the current pairing's own action, only
//     from whatever comes right after it. **Exception: the very first action in the whole
//     session is its own ceiling too** — nothing precedes it on screen, so unlike every later
//     action there's nothing already visible to justify narration bleeding into its reveal;
//     the intro must finish before the first action is shown, not during it. Ceiling/floor
//     were originally scoped to only the next/previous *paired* action (the recorded design
//     decision at the time); that turned out to be insufficient — a silent action between two
//     narrated ones was left completely unprotected, so a cue could legitimately talk over an
//     action nobody was narrating. Rescoped to "the next action, full stop" so what's being
//     narrated and what happens next stay clearly separated regardless of whether the next
//     thing has its own narration.
//   - the FLOOR is max(the previous action's own output end — again, any action, not just a
//     narrated one — and the previous cue's own audio output end) — narration can never start
//     before either, so consecutive cues never overlap each other and a cue never runs ahead
//     of an action the viewer hasn't seen yet.
//   - when ceiling and floor conflict — the floor forces a start so late the audio would still
//     be running once the ceiling's own moment arrives — a held (freeze) segment is inserted
//     right before whatever the ceiling is (normally the next action; the current one itself,
//     for the first-action exception above), delaying its reveal until the narration finishes,
//     and the timeline is rebuilt before the next pairing is placed. A freeze inserted for
//     pairing i shifts pairing i+1's own ceiling/floor, so this can't be precomputed as a
//     batch the way computeTypingOverrides is — it has to interleave with placement, strictly
//     in pairing order.
//   - a pairing with Unit == nil is a **trailing cue** — narration with nothing left to
//     explain (a wrap-up after the last recorded action). It is never dropped: placed
//     immediately after whatever came before it (the floor, same as any other pairing) and
//     the video is extended with a tail freeze to fit its full length, since by definition
//     there is no more real footage after the current end to trim from.
//
// units is the FULL pairable-unit sequence (computePairableUnits(actions), not filtered to
// paired ones) — the same slice pairVoiceCues was given, so each pairing's own Unit is one of
// its elements by identity, letting the loop below locate "the next action" regardless of
// pairing. rawSegments is planSegments' own output (plan.Segments) — the pause/typing/
// network-wait scheduling already done there is the starting point and is never touched here;
// this function only ever adds held segments on top of it. Returns the (possibly
// freeze-extended) timeline alongside the placed cues, since every later stage (cursor,
// camera, annotations, audio) must see the same, final timeline — Direct() takes this timeline
// rather than building one itself.
func planVoicePlacement(pairings []voicePairing, units []pairableUnit, rawSegments []RawSegment, vc VoiceConfig) ([]Segment, []PlacedVoiceCue) {
	segs := append([]RawSegment(nil), rawSegments...)
	timeline := buildTimeline(segs)
	placed := make([]PlacedVoiceCue, 0, len(pairings))
	prevCueAudioOutputEnd := 0.0

	unitIdx := 0
	for _, p := range pairings {
		cue := p.Cue

		if p.Unit == nil {
			currentEnd := outputDurationMs(timeline)
			audioStart := math.Max(currentEnd, prevCueAudioOutputEnd)
			audioEnd := audioStart + cue.TTSDurationMs

			tailAnchor := 0.0
			if n := len(timeline); n > 0 {
				tailAnchor = timeline[n-1].SourceEndMs
			}
			segs = append(segs, RawSegment{SourceStartMs: tailAnchor, SourceEndMs: tailAnchor, HoldMs: audioEnd - currentEnd})
			timeline = buildTimeline(segs)

			placed = append(placed, PlacedVoiceCue{
				TMs:        int64(math.Round(audioStart)),
				AudioFile:  cue.AudioFile,
				DurationMs: int64(math.Round(cue.TTSDurationMs)),
			})
			prevCueAudioOutputEnd = audioEnd
			continue
		}

		unit := *p.Unit
		if !isSourceTimeKept(unit.StartMs, timeline) {
			// Structurally shouldn't happen — meaningful actions are always kept by
			// pause_classifier's own design — but guarded defensively the same way
			// planAudio guards its own action-instant lookups.
			continue
		}

		// Advance to this pairing's own position in the FULL unit sequence — units[unitIdx]
		// is now identically p.Unit — so units[unitIdx-1]/units[unitIdx+1] are the immediately
		// adjacent actions regardless of whether either has a cue.
		for unitIdx < len(units) && units[unitIdx].StartMs < unit.StartMs {
			unitIdx++
		}

		annotatedStartOut := sourceTimeToOutputTime(unit.StartMs, timeline)

		prevActionOutputEnd := 0.0
		if unitIdx > 0 {
			prevActionOutputEnd = sourceTimeToOutputTime(units[unitIdx-1].EndMs, timeline)
		}

		hasNext := unitIdx+1 < len(units)
		ceiling := math.Inf(1)
		freezeAtIdx := unitIdx + 1
		if hasNext {
			ceiling = sourceTimeToOutputTime(units[unitIdx+1].StartMs, timeline)
		}
		if unitIdx == 0 && annotatedStartOut < ceiling {
			// The very first action in the whole session has nothing before it already on
			// screen, unlike every other pairing — its own reveal becomes the ceiling too, and
			// the freeze (if needed) delays THIS action, not the next one.
			ceiling = annotatedStartOut
			freezeAtIdx = unitIdx
		}

		candidateStart := sourceTimeToOutputTime(cue.SourceStartMs, timeline)
		candidateEnd := candidateStart + cue.TTSDurationMs

		target := candidateStart
		switch {
		case candidateEnd > ceiling:
			// Would still be talking once the ceiling's own moment begins — pull earlier so
			// the audio finishes exactly on time.
			target = ceiling - cue.TTSDurationMs
		case candidateEnd < annotatedStartOut:
			// Would finish before its OWN action even starts (dead air) — push later so the
			// end lines up with the action it explains.
			target = annotatedStartOut - cue.TTSDurationMs
		}
		if maxStart := annotatedStartOut - vc.DefaultHoldMs; target > maxStart {
			target = maxStart
		}

		floor := math.Max(prevActionOutputEnd, prevCueAudioOutputEnd)
		audioStart := math.Max(target, floor)
		audioEnd := audioStart + cue.TTSDurationMs

		// The ordinary case only (never the first-action exception, where starting before
		// the ceiling — the action itself — is the entire point of an intro): when the cue
		// is going to need a freeze no matter where exactly it starts (it's simply longer
		// than the gap between its own action and the next one), starting substantially
		// earlier than the action's own reveal buys nothing — the ceiling overrun still has
		// to be frozen through either way — while dragging the narration back over whatever
		// unrelated real footage preceded it. Prefer aligning with the action instead.
		if freezeAtIdx != unitIdx && audioStart < annotatedStartOut && audioEnd > ceiling {
			audioStart = math.Max(annotatedStartOut, floor)
			audioEnd = audioStart + cue.TTSDurationMs
		}

		if audioEnd > ceiling && freezeAtIdx < len(units) {
			// Even pulled as early as the floor allows, it still overruns — freeze the
			// ceiling's own reveal until the narration catches up, then carry on as if that
			// had been the timeline all along.
			segs = append(segs, RawSegment{
				SourceStartMs: units[freezeAtIdx].StartMs,
				SourceEndMs:   units[freezeAtIdx].StartMs,
				HoldMs:        audioEnd - ceiling,
			})
			timeline = buildTimeline(segs)
		}

		placed = append(placed, PlacedVoiceCue{
			TMs:        int64(math.Round(audioStart)),
			AudioFile:  cue.AudioFile,
			DurationMs: int64(math.Round(cue.TTSDurationMs)),
		})

		prevCueAudioOutputEnd = audioEnd
		unitIdx++
	}
	return timeline, placed
}
