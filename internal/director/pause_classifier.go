package director

import (
	"math"
	"sort"
)

// Decides which source time survives, which is removed and which is accelerated (spec 15).
// This is the core MVP behavior; every rule below is a pure function of the raw session.
// Direct port of src/director/pause-classifier.js.

// Actions that carry the story. Pointer samples deliberately do not: moving the mouse
// while thinking is exactly the noise this pipeline exists to remove.
var meaningfulKinds = map[string]bool{
	"click": true, "input": true, "shortcut": true, "scroll": true, "drag": true,
}

func meaningfulActions(events []Event) []Action {
	actions := make([]Action, 0, len(events))
	for _, e := range events {
		if !meaningfulKinds[e.Kind] {
			continue
		}
		startMs := e.T
		if e.StartMs != nil {
			startMs = *e.StartMs
		}
		endMs := e.T
		if e.EndMs != nil {
			endMs = *e.EndMs
		}
		actions = append(actions, Action{Kind: e.Kind, StartMs: startMs, EndMs: endMs, Event: e})
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].StartMs != actions[j].StartMs {
			return actions[i].StartMs < actions[j].StartMs
		}
		return actions[i].EndMs < actions[j].EndMs
	})
	return actions
}

func targetKey(event Event) *string {
	if event.Target == nil {
		return nil
	}
	key := event.Target.Role + "|" + event.Target.Label
	return &key
}

// isTypingGap is true when prev and next are both input events on the same field — the
// predicate computePairableUnits uses to grow a typing run. It plays no role in gap
// classification any more: a typing run is collapsed into a single pairableUnit before
// planSegments ever sees it, so there are no more inter-keystroke gaps to classify.
func isTypingGap(prev, next *Action) bool {
	if prev == nil || next == nil {
		return false
	}
	if prev.Kind != "input" || next.Kind != "input" {
		return false
	}
	a := targetKey(prev.Event)
	b := targetKey(next.Event)
	return a != nil && b != nil && *a == *b
}

// pairableUnit is either one action or a contiguous same-field typing run collapsed into one
// unit — the source of truth for both timeline segmentation (planSegments) and voice-cue
// pairing (voice.go's pairVoiceCues): a typing run is one logical span for both purposes, not
// a sequence of independent keystrokes.
type pairableUnit struct {
	StartMs       float64
	EndMs         float64
	ActionIndexes []int // indexes into the actions slice this unit spans; len 1 unless typing
}

func computePairableUnits(actions []Action) []pairableUnit {
	var units []pairableUnit
	i := 0
	for i < len(actions) {
		if actions[i].Kind != "input" {
			units = append(units, pairableUnit{StartMs: actions[i].StartMs, EndMs: actions[i].EndMs, ActionIndexes: []int{i}})
			i++
			continue
		}
		j := i
		for j+1 < len(actions) && isTypingGap(&actions[j], &actions[j+1]) {
			j++
		}
		indexes := make([]int, 0, j-i+1)
		for k := i; k <= j; k++ {
			indexes = append(indexes, k)
		}
		units = append(units, pairableUnit{StartMs: actions[i].StartMs, EndMs: actions[j].EndMs, ActionIndexes: indexes})
		i = j + 1
	}
	return units
}

// GapDecision describes how to handle a gap between two actions.
type GapDecision struct {
	StartMs  float64
	EndMs    float64
	Reason   string
	Segments []RawSegment
}

func keepWhole(startMs, endMs float64, reason string) GapDecision {
	return GapDecision{
		StartMs: startMs, EndMs: endMs, Reason: reason,
		Segments: []RawSegment{{SourceStartMs: startMs, SourceEndMs: endMs, Speed: 1}},
	}
}

func compress(startMs, endMs, keepHeadMs, keepTailMs, speed float64, reason string) GapDecision {
	middleStart := startMs + keepHeadMs
	middleEnd := endMs - keepTailMs
	if middleEnd-middleStart <= 0 {
		return keepWhole(startMs, endMs, reason+"-too-short")
	}
	return GapDecision{
		StartMs: startMs, EndMs: endMs, Reason: reason,
		Segments: []RawSegment{
			{SourceStartMs: startMs, SourceEndMs: middleStart, Speed: 1},
			{SourceStartMs: middleStart, SourceEndMs: middleEnd, Speed: speed},
			{SourceStartMs: middleEnd, SourceEndMs: endMs, Speed: 1},
		},
	}
}

func classifyNetworkWait(startMs, endMs float64, config Config) GapDecision {
	nc := config.Network
	middle := endMs - startMs - nc.KeepHeadMs - nc.KeepTailMs
	if middle <= 0 {
		return keepWhole(startMs, endMs, "network-wait-short")
	}
	// Aim for roughly a second and a half of visible wait however long the request was,
	// but never slower than the baseline 4x compression.
	speed := math.Min(nc.MaxSpeed, math.Max(nc.MinSpeed, middle/nc.TargetMiddleMs))
	return compress(startMs, endMs, nc.KeepHeadMs, nc.KeepTailMs, speed, "network-wait")
}

func classifyHesitation(startMs, endMs float64, config Config) GapDecision {
	pc := config.Pause
	if endMs-startMs <= pc.KeepAfterActionMs+pc.KeepBeforeActionMs {
		return keepWhole(startMs, endMs, "hesitation-short")
	}
	// The middle is dropped outright: the page is static and the final cursor is synthetic,
	// so the cut has nothing to give it away (spec 15.2).
	return GapDecision{
		StartMs: startMs, EndMs: endMs, Reason: "hesitation",
		Segments: []RawSegment{
			{SourceStartMs: startMs, SourceEndMs: startMs + pc.KeepAfterActionMs, Speed: 1},
			{SourceStartMs: endMs - pc.KeepBeforeActionMs, SourceEndMs: endMs, Speed: 1},
		},
	}
}

// classifyGap decides what happens to the gap between two consecutive pairable units. Typing
// no longer has a special case here: a typing run's own inter-keystroke gaps are absorbed
// into the run's single segment by planSegments before classifyGap is ever called, so every
// gap this function sees is a real gap between two distinct units.
func classifyGap(startMs, endMs float64, busy []Interval, config Config) *GapDecision {
	length := endMs - startMs
	if length <= 0 {
		return nil
	}

	if length <= config.Pause.NaturalPauseMs {
		d := keepWhole(startMs, endMs, "natural")
		return &d
	}

	overlap := overlapMs(startMs, endMs, busy)
	isNetworkWait := overlap >= config.Network.MinOverlapMs && overlap/length >= config.Network.MinOverlapRatio
	if isNetworkWait {
		d := classifyNetworkWait(startMs, endMs, config)
		return &d
	}

	if length > config.Pause.HesitationMs {
		d := classifyHesitation(startMs, endMs, config)
		return &d
	}
	d := keepWhole(startMs, endMs, "natural")
	return &d
}

func classifyHead(endMs float64, config Config) *GapDecision {
	headKeepMs := config.Pause.HeadKeepMs
	if endMs <= headKeepMs {
		d := keepWhole(0, endMs, "head")
		return &d
	}
	// Keep the beat immediately before the first action, drop the dead air before it.
	return &GapDecision{
		StartMs: 0, EndMs: endMs, Reason: "head-trim",
		Segments: []RawSegment{{SourceStartMs: endMs - headKeepMs, SourceEndMs: endMs, Speed: 1}},
	}
}

// The tail is compressed, never truncated. Stopping the recording is itself a deliberate
// act: the operator clicks stop once the thing they were demonstrating has finished, which
// makes the last frame the closing shot rather than dead air.
func classifyTail(startMs, durationMs float64, config Config) *GapDecision {
	tailKeepMs := config.Pause.TailKeepMs
	length := durationMs - startMs
	if length <= 0 {
		return nil
	}
	if length <= tailKeepMs*2 {
		d := keepWhole(startMs, durationMs, "tail")
		return &d
	}

	nc := config.Network
	middle := length - tailKeepMs*2
	speed := math.Min(nc.MaxSpeed, math.Max(nc.MinSpeed, middle/nc.TargetMiddleMs))
	d := compress(startMs, durationMs, tailKeepMs, tailKeepMs, speed, "tail")
	return &d
}

// PlanResult is the output of pause classification.
type PlanResult struct {
	Segments []RawSegment
	Gaps     []GapDecision
}

// planSegments produces the full segment list for the timeline: one segment per pairable
// unit — real speed for an ordinary action, config.Typing.Speed for a whole typing run — plus
// one gap classification between consecutive units (never inside a run, since a run is
// already collapsed to a single unit by computePairableUnits).
func planSegments(units []pairableUnit, durationMs float64, busy []Interval, config Config) PlanResult {
	if len(units) == 0 {
		return PlanResult{
			Segments: []RawSegment{{SourceStartMs: 0, SourceEndMs: durationMs, Speed: 1}},
		}
	}

	var gaps []GapDecision
	var segments []RawSegment

	if head := classifyHead(units[0].StartMs, config); head != nil {
		gaps = append(gaps, *head)
		segments = append(segments, head.Segments...)
	}

	for i := range units {
		unit := units[i]
		speed := 1.0
		if len(unit.ActionIndexes) > 1 {
			speed = config.Typing.Speed
		}
		if unit.EndMs > unit.StartMs {
			segments = append(segments, RawSegment{SourceStartMs: unit.StartMs, SourceEndMs: unit.EndMs, Speed: speed})
		}
		if i+1 >= len(units) {
			break
		}
		gap := classifyGap(unit.EndMs, units[i+1].StartMs, busy, config)
		if gap != nil {
			gaps = append(gaps, *gap)
			segments = append(segments, gap.Segments...)
		}
	}

	last := units[len(units)-1]
	if tail := classifyTail(last.EndMs, math.Max(durationMs, last.EndMs), config); tail != nil {
		gaps = append(gaps, *tail)
		segments = append(segments, tail.Segments...)
	}

	return PlanResult{Segments: segments, Gaps: gaps}
}
