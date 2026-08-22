package director

import (
	"math"
	"sort"
)

// Turns raw request records into the "network busy" union used by pause classification
// (spec 13.2). Timing only — the Director never sees URLs, headers or bodies.
// Direct port of src/director/network-intervals.js.

// Interval is a time range.
type Interval struct {
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
}

func relevantRequests(network []NetworkRecord, config NetworkConfig) []NetworkRecord {
	types := make(map[string]bool, len(config.RelevantTypes))
	for _, t := range config.RelevantTypes {
		types[t] = true
	}
	out := make([]NetworkRecord, 0, len(network))
	for _, r := range network {
		if !types[r.Type] {
			continue
		}
		duration := r.EndMs - r.StartMs
		if duration <= 0 {
			continue
		}
		// A permanently open connection must not mark the whole video as a wait.
		if duration > config.MaxRequestMs {
			continue
		}
		out = append(out, r)
	}
	return out
}

func mergeIntervals(intervals []Interval, mergeGapMs float64) []Interval {
	sorted := append([]Interval{}, intervals...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartMs < sorted[j].StartMs })

	merged := make([]Interval, 0, len(sorted))
	for _, interval := range sorted {
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if interval.StartMs-last.EndMs <= mergeGapMs {
				if interval.EndMs > last.EndMs {
					last.EndMs = interval.EndMs
				}
				continue
			}
		}
		merged = append(merged, interval)
	}
	return merged
}

func networkBusyIntervals(network []NetworkRecord, config NetworkConfig) []Interval {
	relevant := relevantRequests(network, config)
	intervals := make([]Interval, len(relevant))
	for i, r := range relevant {
		intervals[i] = Interval{StartMs: r.StartMs, EndMs: r.EndMs}
	}
	return mergeIntervals(intervals, config.MergeGapMs)
}

func overlapMs(startMs, endMs float64, intervals []Interval) float64 {
	total := 0.0
	for _, iv := range intervals {
		overlap := math.Min(endMs, iv.EndMs) - math.Max(startMs, iv.StartMs)
		if overlap > 0 {
			total += overlap
		}
	}
	return total
}
