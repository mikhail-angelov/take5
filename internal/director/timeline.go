package director

import (
	"math"
	"sort"
)

// The one place source time becomes output time (spec 18). Every visual feature — cursor,
// camera, annotations — maps through here rather than repeating the arithmetic.
// Direct port of src/director/timeline.js.

const epsilonMs = 0.5

// RawSegment is a timing interval in source time with a playback speed. A held (freeze)
// segment instead has SourceStartMs == SourceEndMs and HoldMs > 0: it doesn't advance the
// source at all, it holds that source instant on screen for HoldMs of output time. Speed is
// meaningless on a held segment and ignored.
type RawSegment struct {
	SourceStartMs float64
	SourceEndMs   float64
	Speed         float64
	HoldMs        float64
}

// Segment is a timing interval with both source and output times. See RawSegment's doc
// comment for what a held (HoldMs > 0) segment means.
type Segment struct {
	SourceStartMs float64
	SourceEndMs   float64
	Speed         float64
	HoldMs        float64
	OutputStartMs float64
	OutputEndMs   float64
}

// insertHeldSegment appends a held (freeze) segment to an already-merged timeline prefix. A
// held instant can land in the middle of the last merged real segment — planVoicePlacement
// doesn't know or care how planSegments happened to chunk the surrounding footage — so that
// segment is split around it rather than the tail being dropped on the floor: the freeze can
// be inserted anywhere, not just at a boundary the caller happened to already have. A held
// segment is never merged with a neighbor, held or not — each freeze is its own entry.
func insertHeldSegment(merged []Segment, segment RawSegment) []Segment {
	if n := len(merged); n > 0 {
		last := &merged[n-1]
		if last.HoldMs <= 0 && segment.SourceStartMs < last.SourceEndMs {
			tailEndMs, tailSpeed := last.SourceEndMs, last.Speed
			last.SourceEndMs = segment.SourceStartMs
			merged = append(merged, Segment{SourceStartMs: segment.SourceStartMs, SourceEndMs: segment.SourceStartMs, HoldMs: segment.HoldMs})
			if tailEndMs-segment.SourceStartMs > epsilonMs {
				merged = append(merged, Segment{SourceStartMs: segment.SourceStartMs, SourceEndMs: tailEndMs, Speed: tailSpeed})
			}
			return merged
		}
	}
	return append(merged, Segment{SourceStartMs: segment.SourceStartMs, SourceEndMs: segment.SourceStartMs, HoldMs: segment.HoldMs})
}

// buildTimeline normalises raw segments into an ascending, non-overlapping timeline with
// output offsets. Adjacent segments that are contiguous in the source and share a speed are
// merged so the FFmpeg pass gets the smallest possible concat list.
func buildTimeline(segments []RawSegment) []Segment {
	sorted := make([]RawSegment, 0, len(segments))
	for _, s := range segments {
		// A held segment is zero-width by design (SourceStartMs == SourceEndMs), not by
		// degeneracy — it must survive this filter regardless of the epsilon check.
		if s.HoldMs > 0 || s.SourceEndMs-s.SourceStartMs > epsilonMs {
			sorted = append(sorted, s)
		}
	}
	// sort.SliceStable, not sort.Slice — see docs/plans/go-port.md's traps: ties on
	// sourceStartMs must keep arrival order, as Array.prototype.sort guarantees in Node. The
	// one exception: a held segment sharing a sourceStartMs with a real segment always sorts
	// first — the freeze plays before the delayed segment becomes visible, never after,
	// regardless of which order the caller appended them in.
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].SourceStartMs != sorted[j].SourceStartMs {
			return sorted[i].SourceStartMs < sorted[j].SourceStartMs
		}
		return sorted[i].HoldMs > 0 && sorted[j].HoldMs <= 0
	})

	merged := make([]Segment, 0, len(sorted))
	for _, segment := range sorted {
		if segment.HoldMs > 0 {
			merged = insertHeldSegment(merged, segment)
			continue
		}

		speed := segment.Speed
		if !(speed > 0) {
			speed = 1
		}
		sourceStartMs := segment.SourceStartMs
		if n := len(merged); n > 0 {
			sourceStartMs = math.Max(segment.SourceStartMs, merged[n-1].SourceEndMs)
		}
		// Overlapping inputs (an action span inside a gap, say) are clipped rather than
		// double-counted, so the same source frame is never emitted twice.
		if segment.SourceEndMs-sourceStartMs <= epsilonMs {
			continue
		}

		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if last.HoldMs <= 0 && last.Speed == speed && math.Abs(last.SourceEndMs-sourceStartMs) <= epsilonMs {
				last.SourceEndMs = segment.SourceEndMs
				continue
			}
		}
		merged = append(merged, Segment{SourceStartMs: sourceStartMs, SourceEndMs: segment.SourceEndMs, Speed: speed})
	}

	outputStartMs := 0.0
	for i := range merged {
		duration := merged[i].HoldMs
		if merged[i].HoldMs <= 0 {
			duration = (merged[i].SourceEndMs - merged[i].SourceStartMs) / merged[i].Speed
		}
		merged[i].OutputStartMs = outputStartMs
		merged[i].OutputEndMs = outputStartMs + duration
		outputStartMs = merged[i].OutputEndMs
	}
	return merged
}

func outputDurationMs(timeline []Segment) float64 {
	if len(timeline) == 0 {
		return 0
	}
	return timeline[len(timeline)-1].OutputEndMs
}

func sourceDurationMs(timeline []Segment) float64 {
	sum := 0.0
	for _, s := range timeline {
		sum += s.SourceEndMs - s.SourceStartMs
	}
	return sum
}

// sourceTimeToOutputTime maps a source instant to output time. Source instants that were
// cut collapse onto the cut point, which is exactly what a viewer sees: the removed span
// simply does not exist in the output. An instant that lands exactly on a held (freeze)
// segment resolves to the far side of the hold — whatever happens at that source instant
// only becomes visible once the freeze (and the narration playing over it) has finished, not
// at the moment the freeze begins.
func sourceTimeToOutputTime(sourceMs float64, timeline []Segment) float64 {
	if len(timeline) == 0 {
		return 0
	}
	if sourceMs <= timeline[0].SourceStartMs {
		return 0
	}
	for i, segment := range timeline {
		if segment.HoldMs > 0 {
			if sourceMs <= segment.SourceStartMs {
				return segment.OutputEndMs
			}
			continue
		}
		if sourceMs < segment.SourceStartMs {
			return segment.OutputStartMs
		}
		if sourceMs < segment.SourceEndMs {
			return segment.OutputStartMs + (sourceMs-segment.SourceStartMs)/segment.Speed
		}
		if sourceMs == segment.SourceEndMs {
			// A held segment sharing this exact boundary always sorts immediately after
			// (buildTimeline's tie-break) — defer to its own branch above rather than
			// claiming the instant here, or the freeze would never be observable.
			if i+1 < len(timeline) && timeline[i+1].HoldMs > 0 && timeline[i+1].SourceStartMs == sourceMs {
				continue
			}
			return segment.OutputEndMs
		}
	}
	return outputDurationMs(timeline)
}

// isSourceTimeKept is true when the instant survives the edit. A held instant (SourceStartMs
// == SourceEndMs) counts as kept — it's a real, if paused, moment on the output timeline.
func isSourceTimeKept(sourceMs float64, timeline []Segment) bool {
	for _, s := range timeline {
		if sourceMs >= s.SourceStartMs && sourceMs <= s.SourceEndMs {
			return true
		}
	}
	return false
}
