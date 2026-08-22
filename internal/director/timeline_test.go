package director

import "testing"

// Port of test/timeline.test.js.

// The scenario named in spec 30.2:
//
//	source  0-10 s at 1x   -> output  0-10 s
//	source 10-18 s at 4x   -> output 10-12 s
//	source 18-20 s removed
//	source 20-25 s at 1x   -> output 12-17 s
func testTimeline() []Segment {
	return buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 10000, Speed: 1},
		{SourceStartMs: 10000, SourceEndMs: 18000, Speed: 4},
		{SourceStartMs: 20000, SourceEndMs: 25000, Speed: 1},
	})
}

func TestBuildTimelineAssignsCumulativeOutputOffsets(t *testing.T) {
	tl := testTimeline()
	want := [][2]float64{{0, 10000}, {10000, 12000}, {12000, 17000}}
	if len(tl) != len(want) {
		t.Fatalf("len = %d, want %d", len(tl), len(want))
	}
	for i, s := range tl {
		if s.OutputStartMs != want[i][0] || s.OutputEndMs != want[i][1] {
			t.Errorf("segment %d = [%v, %v], want %v", i, s.OutputStartMs, s.OutputEndMs, want[i])
		}
	}
}

func TestBuildTimelineReportsOutputAndKeptSourceDurations(t *testing.T) {
	tl := testTimeline()
	if got := outputDurationMs(tl); got != 17000 {
		t.Errorf("outputDurationMs = %v, want 17000", got)
	}
	if got := sourceDurationMs(tl); got != 23000 {
		t.Errorf("sourceDurationMs = %v, want 23000", got)
	}
}

func TestBuildTimelineMergesContiguousSegmentsThatShareASpeed(t *testing.T) {
	merged := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 500, Speed: 1},
		{SourceStartMs: 500, SourceEndMs: 900, Speed: 1},
		{SourceStartMs: 900, SourceEndMs: 1500, Speed: 2},
	})
	want := [][3]float64{{0, 900, 1}, {900, 1500, 2}}
	if len(merged) != len(want) {
		t.Fatalf("len = %d, want %d", len(merged), len(want))
	}
	for i, s := range merged {
		if s.SourceStartMs != want[i][0] || s.SourceEndMs != want[i][1] || s.Speed != want[i][2] {
			t.Errorf("segment %d = %+v, want %v", i, s, want[i])
		}
	}
}

func TestBuildTimelineDoesNotMergeSegmentsSeparatedByACut(t *testing.T) {
	kept := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 500, Speed: 1},
		{SourceStartMs: 3000, SourceEndMs: 3500, Speed: 1},
	})
	if len(kept) != 2 {
		t.Fatalf("len = %d, want 2", len(kept))
	}
	if got := outputDurationMs(kept); got != 1000 {
		t.Errorf("outputDurationMs = %v, want 1000", got)
	}
}

func TestBuildTimelineDropsEmptySegmentsAndClipsOverlappingOnes(t *testing.T) {
	clipped := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 1000, Speed: 1},
		{SourceStartMs: 1000, SourceEndMs: 1000, Speed: 1},
		{SourceStartMs: 800, SourceEndMs: 1600, Speed: 2},
	})
	want := [][3]float64{{0, 1000, 1}, {1000, 1600, 2}}
	if len(clipped) != len(want) {
		t.Fatalf("len = %d, want %d", len(clipped), len(want))
	}
	for i, s := range clipped {
		if s.SourceStartMs != want[i][0] || s.SourceEndMs != want[i][1] || s.Speed != want[i][2] {
			t.Errorf("segment %d = %+v, want %v", i, s, want[i])
		}
	}
}

func TestBuildTimelineSortsUnorderedInput(t *testing.T) {
	sorted := buildTimeline([]RawSegment{
		{SourceStartMs: 5000, SourceEndMs: 6000, Speed: 1},
		{SourceStartMs: 0, SourceEndMs: 1000, Speed: 1},
	})
	if sorted[0].SourceStartMs != 0 {
		t.Errorf("sorted[0].SourceStartMs = %v, want 0", sorted[0].SourceStartMs)
	}
}

func TestSourceTimeToOutputTime(t *testing.T) {
	tl := testTimeline()
	cases := []struct {
		name     string
		sourceMs float64
		want     float64
	}{
		{"at the very start", 0, 0},
		{"inside the first 1x segment", 5000, 5000},
		{"at the boundary into the fast segment", 10000, 10000},
		{"a quarter into the fast segment", 12000, 10500},
		{"halfway through the fast segment", 14000, 11000},
		{"at the end of the fast segment", 18000, 12000},
		{"inside the removed span", 19000, 12000},
		{"at the end of the removed span", 20000, 12000},
		{"inside the final 1x segment", 22500, 14500},
		{"at the very end", 25000, 17000},
		{"past the end of the recording", 40000, 17000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sourceTimeToOutputTime(c.sourceMs, tl); got != c.want {
				t.Errorf("sourceTimeToOutputTime(%v) = %v, want %v", c.sourceMs, got, c.want)
			}
		})
	}
}

func TestSourceTimeToOutputTimeClampsBeforeFirstKeptSegment(t *testing.T) {
	trimmed := buildTimeline([]RawSegment{{SourceStartMs: 4000, SourceEndMs: 6000, Speed: 1}})
	if got := sourceTimeToOutputTime(0, trimmed); got != 0 {
		t.Errorf("at 0 = %v, want 0", got)
	}
	if got := sourceTimeToOutputTime(4000, trimmed); got != 0 {
		t.Errorf("at 4000 = %v, want 0", got)
	}
	if got := sourceTimeToOutputTime(5000, trimmed); got != 1000 {
		t.Errorf("at 5000 = %v, want 1000", got)
	}
}

func TestSourceTimeToOutputTimeIsMonotonicallyNonDecreasing(t *testing.T) {
	tl := testTimeline()
	previous := -1.0
	for tMs := 0.0; tMs <= 26000; tMs += 250 {
		output := sourceTimeToOutputTime(tMs, tl)
		if output < previous {
			t.Fatalf("regressed at %v ms: %v < %v", tMs, output, previous)
		}
		previous = output
	}
}

func TestSourceTimeToOutputTimeReturnsZeroForEmptyTimeline(t *testing.T) {
	if got := sourceTimeToOutputTime(1000, nil); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

// heldTimeline mirrors testTimeline's shape but inserts a 2s freeze exactly where the first
// segment ends and the second begins (source 10000), so kept/removed boundaries and merge
// eligibility are exercised the same way testTimeline's own cases are.
func heldTimeline() []Segment {
	return buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 10000, Speed: 1},
		{SourceStartMs: 10000, SourceEndMs: 10000, HoldMs: 2000},
		{SourceStartMs: 10000, SourceEndMs: 18000, Speed: 4},
	})
}

func TestBuildTimelineInsertsAHeldSegmentWithoutMergingAcrossIt(t *testing.T) {
	tl := heldTimeline()
	want := []struct {
		sourceStart, sourceEnd, holdMs, outputStart, outputEnd float64
	}{
		{0, 10000, 0, 0, 10000},
		{10000, 10000, 2000, 10000, 12000},
		{10000, 18000, 0, 12000, 14000},
	}
	if len(tl) != len(want) {
		t.Fatalf("len = %d, want %d: %+v", len(tl), len(want), tl)
	}
	for i, w := range want {
		s := tl[i]
		if s.SourceStartMs != w.sourceStart || s.SourceEndMs != w.sourceEnd || s.HoldMs != w.holdMs ||
			s.OutputStartMs != w.outputStart || s.OutputEndMs != w.outputEnd {
			t.Errorf("segment %d = %+v, want %+v", i, s, w)
		}
	}
}

func TestBuildTimelineSplitsAWideSegmentAroundAHeldInstantInsideIt(t *testing.T) {
	// A single flat segment spanning the whole recording (the common case for a still page)
	// with a held instant landing in the middle of it, not at a pre-existing boundary — the
	// caller (planVoicePlacement) has no idea how planSegments happened to chunk things, so
	// buildTimeline must split the containing segment itself.
	tl := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 5000, Speed: 1},
		{SourceStartMs: 1500, SourceEndMs: 1500, HoldMs: 400},
	})
	want := []struct {
		sourceStart, sourceEnd, holdMs, outputStart, outputEnd float64
	}{
		{0, 1500, 0, 0, 1500},
		{1500, 1500, 400, 1500, 1900},
		{1500, 5000, 0, 1900, 5400},
	}
	if len(tl) != len(want) {
		t.Fatalf("len = %d, want %d: %+v", len(tl), len(want), tl)
	}
	for i, w := range want {
		s := tl[i]
		if s.SourceStartMs != w.sourceStart || s.SourceEndMs != w.sourceEnd || s.HoldMs != w.holdMs ||
			s.OutputStartMs != w.outputStart || s.OutputEndMs != w.outputEnd {
			t.Errorf("segment %d = %+v, want %+v", i, s, w)
		}
	}
}

func TestBuildTimelineNeverMergesAHeldSegmentWithANeighbor(t *testing.T) {
	// Same speed on both sides of the hold, contiguous in source time — a same-speed merge
	// would normally fire here (TestBuildTimelineMergesContiguousSegmentsThatShareASpeed) but
	// must not cross a held segment.
	tl := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 1000, Speed: 1},
		{SourceStartMs: 1000, SourceEndMs: 1000, HoldMs: 500},
		{SourceStartMs: 1000, SourceEndMs: 2000, Speed: 1},
	})
	if len(tl) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(tl), tl)
	}
	if tl[1].HoldMs != 500 {
		t.Errorf("held segment HoldMs = %v, want 500", tl[1].HoldMs)
	}
}

func TestSourceTimeToOutputTimeResolvesAHeldBoundaryToPostHold(t *testing.T) {
	tl := heldTimeline()
	// Exactly at the shared boundary: must resolve past the 2s hold (12000), not to the
	// pre-hold instant (10000) the first segment's own end would otherwise claim.
	if got := sourceTimeToOutputTime(10000, tl); got != 12000 {
		t.Errorf("at 10000 = %v, want 12000 (post-hold)", got)
	}
	// Just inside the segment before the hold: unaffected.
	if got := sourceTimeToOutputTime(9000, tl); got != 9000 {
		t.Errorf("at 9000 = %v, want 9000", got)
	}
	// Just past the hold, inside the following (4x) segment: offset from the post-hold point.
	if got := sourceTimeToOutputTime(10500, tl); got != 12125 {
		t.Errorf("at 10500 = %v, want 12125", got)
	}
}

func TestIsSourceTimeKeptIsTrueAtAHeldInstant(t *testing.T) {
	tl := heldTimeline()
	if !isSourceTimeKept(10000, tl) {
		t.Error("10000 (the held instant) should be kept")
	}
}

func TestIsSourceTimeKeptDistinguishesKeptFromRemoved(t *testing.T) {
	tl := testTimeline()
	if !isSourceTimeKept(9000, tl) {
		t.Error("9000 should be kept")
	}
	if isSourceTimeKept(19000, tl) {
		t.Error("19000 should be removed")
	}
	if !isSourceTimeKept(24000, tl) {
		t.Error("24000 should be kept")
	}
}
