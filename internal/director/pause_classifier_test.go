package director

import "testing"

// Port of test/pause-classifier.test.js.

func clickEvent(t, x, y float64) Event {
	return Event{Kind: "click", T: t, X: new(x), Y: new(y)}
}

func pointerEvent(t, x, y float64) Event {
	return Event{Kind: "pointer", T: t, X: new(x), Y: new(y)}
}

func scrollEvent(startMs, endMs float64) Event {
	return Event{Kind: "scroll", T: startMs, StartMs: new(startMs), EndMs: new(endMs)}
}

func inputEvent(t float64, target Target) Event {
	return Event{Kind: "input", T: t, Target: &target}
}

func networkRequest(endMs float64, reqType string) NetworkRecord {
	return NetworkRecord{StartMs: 0, EndMs: endMs, Type: reqType, Status: 200}
}

var testConfig = DefaultConfig()

type gapArgs struct {
	StartMs, EndMs float64
	Busy           []Interval
}

func gap(a gapArgs) *GapDecision {
	return classifyGap(a.StartMs, a.EndMs, a.Busy, testConfig)
}

// keptMs is the total screen time a decision's segments occupy — real (possibly retimed)
// footage plus any held (frozen) padding.
func keptMs(decision *GapDecision) float64 {
	sum := 0.0
	for _, s := range decision.Segments {
		if s.HoldMs > 0 {
			sum += s.HoldMs
			continue
		}
		sum += (s.SourceEndMs - s.SourceStartMs) / s.Speed
	}
	return sum
}

func TestMeaningfulActionsKeepsStoryCarryingActionsAndDropsPointerNoise(t *testing.T) {
	actions := meaningfulActions([]Event{
		pointerEvent(100, 10, 10),
		clickEvent(200, 20, 20),
		pointerEvent(300, 30, 30),
		scrollEvent(400, 900),
	})
	want := [][3]float64{{0 /* click */, 200, 200}, {1 /* scroll */, 400, 900}}
	if len(actions) != 2 {
		t.Fatalf("len = %d, want 2", len(actions))
	}
	if actions[0].Kind != "click" || actions[0].StartMs != want[0][1] || actions[0].EndMs != want[0][2] {
		t.Errorf("actions[0] = %+v", actions[0])
	}
	if actions[1].Kind != "scroll" || actions[1].StartMs != want[1][1] || actions[1].EndMs != want[1][2] {
		t.Errorf("actions[1] = %+v", actions[1])
	}
}

func TestClassifyGapKeepsAShortIdleGapUnchanged(t *testing.T) {
	decision := gap(gapArgs{StartMs: 1000, EndMs: 1600})
	if decision.Reason != "natural" {
		t.Errorf("reason = %q, want natural", decision.Reason)
	}
	want := []RawSegment{{SourceStartMs: 1000, SourceEndMs: 1600, Speed: 1}}
	if len(decision.Segments) != 1 || decision.Segments[0] != want[0] {
		t.Errorf("segments = %+v, want %+v", decision.Segments, want)
	}
}

func TestClassifyGapKeepsALongButSubHesitationGap(t *testing.T) {
	decision := gap(gapArgs{StartMs: 0, EndMs: 1100})
	if decision.Reason != "natural" {
		t.Errorf("reason = %q, want natural", decision.Reason)
	}
	if got := keptMs(decision); got != 1100 {
		t.Errorf("keptMs = %v, want 1100", got)
	}
}

func TestClassifyGapCutsTheMiddleOutOfAFiveSecondGapWithNoNetworkActivity(t *testing.T) {
	decision := gap(gapArgs{StartMs: 2000, EndMs: 7000})
	if decision.Reason != "hesitation" {
		t.Errorf("reason = %q, want hesitation", decision.Reason)
	}
	want := []RawSegment{
		{SourceStartMs: 2000, SourceEndMs: 2500, Speed: 1},
		{SourceStartMs: 6750, SourceEndMs: 7000, Speed: 1},
	}
	if len(decision.Segments) != 2 || decision.Segments[0] != want[0] || decision.Segments[1] != want[1] {
		t.Errorf("segments = %+v, want %+v", decision.Segments, want)
	}
	// 5 s of hesitation becomes a 750 ms beat.
	if got := keptMs(decision); got != 750 {
		t.Errorf("keptMs = %v, want 750", got)
	}
}

func TestClassifyGapCompressesAnEightSecondGapThatOverlapsAnXHR(t *testing.T) {
	busy := []Interval{{StartMs: 2100, EndMs: 9800}}
	decision := gap(gapArgs{StartMs: 2000, EndMs: 10000, Busy: busy})
	if decision.Reason != "network-wait" {
		t.Errorf("reason = %q, want network-wait", decision.Reason)
	}
	if len(decision.Segments) != 3 {
		t.Fatalf("len(segments) = %d, want 3", len(decision.Segments))
	}
	head, middle, tail := decision.Segments[0], decision.Segments[1], decision.Segments[2]
	if head.SourceEndMs-head.SourceStartMs != 400 || head.Speed != 1 {
		t.Errorf("head = %+v", head)
	}
	if tail.SourceEndMs-tail.SourceStartMs != 600 || tail.Speed != 1 {
		t.Errorf("tail = %+v", tail)
	}
	if middle.Speed < 4 {
		t.Errorf("middle.Speed = %v, want >= 4", middle.Speed)
	}
	if visible := keptMs(decision); !(visible > 1000 && visible < 1600) {
		t.Errorf("visible wait was %v ms", visible)
	}
}

func TestClassifyGapTreatsIncidentalOverlapAsHesitation(t *testing.T) {
	// One brief background request does not make five seconds of thinking a wait.
	busy := []Interval{{StartMs: 2100, EndMs: 2400}}
	decision := gap(gapArgs{StartMs: 2000, EndMs: 7000, Busy: busy})
	if decision.Reason != "hesitation" {
		t.Errorf("reason = %q, want hesitation", decision.Reason)
	}
}

func TestClassifyGapIgnoresAPermanentlyOpenConnection(t *testing.T) {
	network := []NetworkRecord{networkRequest(600000, "xmlhttprequest")}
	busy := networkBusyIntervals(network, testConfig.Network)
	if len(busy) != 0 {
		t.Errorf("busy = %+v, want empty", busy)
	}
	decision := gap(gapArgs{StartMs: 2000, EndMs: 7000, Busy: busy})
	if decision.Reason != "hesitation" {
		t.Errorf("reason = %q, want hesitation", decision.Reason)
	}
}

// --- Typing runs are now collapsed into one pairableUnit before classifyGap ever sees a gap
// (docs/plans/20260822-action-model-and-robust-pass-a.md) — these tests exercise planSegments'
// unit-based segmentation directly rather than classifyGap's now-removed typing branch.

func typingRunActions(field Target, times ...float64) []Action {
	actions := make([]Action, len(times))
	for i, t := range times {
		actions[i] = Action{Kind: "input", StartMs: t, EndMs: t, Event: Event{Kind: "input", Target: &field}}
	}
	return actions
}

func TestPlanSegmentsCollapsesATypingRunIntoOneSegmentAtTheConfiguredSpeed(t *testing.T) {
	field := Target{Role: "textbox", Label: "Project name", Rect: &Rect{X: 0, Y: 0, Width: 300, Height: 40}}
	actions := typingRunActions(field, 1000, 2000, 4000)
	units := computePairableUnits(actions)
	if len(units) != 1 {
		t.Fatalf("len(units) = %d, want 1", len(units))
	}

	result := planSegments(units, 12000, nil, testConfig)
	timeline := buildTimeline(result.Segments)

	found := false
	for _, s := range timeline {
		if s.SourceStartMs == 1000 && s.SourceEndMs == 4000 {
			found = true
			if s.Speed != testConfig.Typing.Speed {
				t.Errorf("speed = %v, want config.Typing.Speed = %v", s.Speed, testConfig.Typing.Speed)
			}
		}
	}
	if !found {
		t.Error("expected one segment spanning the whole typing run (1000..4000)")
	}
}

func TestPlanSegmentsGivesTypingRunsOfAnyKeystrokeCountTheSameSpeed(t *testing.T) {
	field := Target{Role: "textbox", Label: "Project name"}
	short := computePairableUnits(typingRunActions(field, 1000, 1100, 1200))
	long := computePairableUnits(typingRunActions(field, 1000, 1100, 1200, 1300, 1400, 1500, 1600, 1700, 1800, 1900, 2000))

	shortTimeline := buildTimeline(planSegments(short, 5000, nil, testConfig).Segments)
	longTimeline := buildTimeline(planSegments(long, 5000, nil, testConfig).Segments)

	speedOf := func(timeline []Segment, startMs float64) float64 {
		for _, s := range timeline {
			if s.SourceStartMs == startMs {
				return s.Speed
			}
		}
		t.Fatalf("no segment starting at %v", startMs)
		return 0
	}
	if got := speedOf(shortTimeline, short[0].StartMs); got != testConfig.Typing.Speed {
		t.Errorf("short run speed = %v, want %v", got, testConfig.Typing.Speed)
	}
	if got := speedOf(longTimeline, long[0].StartMs); got != testConfig.Typing.Speed {
		t.Errorf("long run speed = %v, want %v", got, testConfig.Typing.Speed)
	}
}

func TestPlanSegmentsDoesNotTreatPausesBetweenDifferentFieldsAsOneTypingRun(t *testing.T) {
	name := Target{Role: "textbox", Label: "Name"}
	email := Target{Role: "textbox", Label: "Email"}
	actions := []Action{
		{Kind: "input", StartMs: 1000, EndMs: 1000, Event: Event{Kind: "input", Target: &name}},
		{Kind: "input", StartMs: 4000, EndMs: 4000, Event: Event{Kind: "input", Target: &email}},
	}
	units := computePairableUnits(actions)
	if len(units) != 2 {
		t.Fatalf("len(units) = %d, want 2 (different fields must not merge into one run)", len(units))
	}
	// The gap between the two single-keystroke units goes through the ordinary gap
	// classification, same as any other pair of actions.
	decision := gap(gapArgs{StartMs: units[0].EndMs, EndMs: units[1].StartMs})
	if decision.Reason != "hesitation" {
		t.Errorf("reason = %q, want hesitation", decision.Reason)
	}
}

func TestClassifyGapReturnsNothingForAZeroLengthGap(t *testing.T) {
	if decision := gap(gapArgs{StartMs: 1000, EndMs: 1000}); decision != nil {
		t.Errorf("decision = %+v, want nil", decision)
	}
}

func TestPlanSegmentsTrimsDeadAirButCompressesTheTailToTheVeryEnd(t *testing.T) {
	actions := meaningfulActions([]Event{clickEvent(5000, 10, 10), clickEvent(5600, 20, 20)})
	result := planSegments(computePairableUnits(actions), 12000, nil, testConfig)
	timeline := buildTimeline(result.Segments)
	last := timeline[len(timeline)-1]

	if timeline[0].SourceStartMs != 5000-testConfig.Pause.HeadKeepMs {
		t.Errorf("first segment start = %v", timeline[0].SourceStartMs)
	}
	if last.SourceEndMs != 12000 {
		t.Errorf("last.SourceEndMs = %v, want 12000", last.SourceEndMs)
	}
	if last.Speed != 1 {
		t.Errorf("last.Speed = %v, want 1", last.Speed)
	}
	sped := false
	for _, s := range timeline {
		if s.Speed > 1 {
			sped = true
		}
	}
	if !sped {
		t.Error("the dead air in the middle of the tail should be sped up, not kept")
	}
	if output := outputDurationMs(timeline); !(output < (12000-4400)/2) {
		t.Errorf("output was %v ms", output)
	}
}

func TestPlanSegmentsKeepsAShortTailWhole(t *testing.T) {
	actions := meaningfulActions([]Event{clickEvent(5000, 10, 10)})
	result := planSegments(computePairableUnits(actions), 6000, nil, testConfig)
	timeline := buildTimeline(result.Segments)
	if timeline[len(timeline)-1].SourceEndMs != 6000 {
		t.Errorf("last.SourceEndMs = %v, want 6000", timeline[len(timeline)-1].SourceEndMs)
	}
	for _, s := range timeline {
		if s.Speed != 1 {
			t.Errorf("segment speed = %v, want 1", s.Speed)
		}
	}
}

func TestPlanSegmentsPlaysScrollGesturesAtRealSpeed(t *testing.T) {
	actions := meaningfulActions([]Event{scrollEvent(1000, 1800)})
	result := planSegments(computePairableUnits(actions), 4000, nil, testConfig)
	timeline := buildTimeline(result.Segments)
	covers := false
	for _, s := range timeline {
		if s.SourceStartMs <= 1000 && s.SourceEndMs >= 1800 && s.Speed == 1 {
			covers = true
		}
	}
	if !covers {
		t.Error("expected a real-speed segment covering the scroll")
	}
}

func TestPlanSegmentsKeepsTheWholeRecordingWhenNothingMeaningfulHappened(t *testing.T) {
	result := planSegments(nil, 9000, nil, testConfig)
	want := []RawSegment{{SourceStartMs: 0, SourceEndMs: 9000, Speed: 1}}
	if len(result.Segments) != 1 || result.Segments[0] != want[0] {
		t.Errorf("segments = %+v, want %+v", result.Segments, want)
	}
}

func TestPlanSegmentsProducesAStrictlyOrderedNonOverlappingTimelineForAMixedSession(t *testing.T) {
	actions := meaningfulActions([]Event{
		clickEvent(3000, 100, 100),
		inputEvent(9000, Target{Label: "Name", Rect: &Rect{X: 0, Y: 0, Width: 200, Height: 40}}),
		scrollEvent(11000, 11500),
		clickEvent(20000, 300, 300),
	})
	result := planSegments(computePairableUnits(actions), 26000, []Interval{{StartMs: 20100, EndMs: 25000}}, testConfig)
	timeline := buildTimeline(result.Segments)

	for i := 1; i < len(timeline); i++ {
		if timeline[i].SourceStartMs < timeline[i-1].SourceEndMs {
			t.Errorf("segment %d starts before segment %d ends", i, i-1)
		}
	}
	if output := outputDurationMs(timeline); !(output < 26000) {
		t.Errorf("outputDurationMs = %v, want < 26000", output)
	}
}

func TestNetworkIntervalsUnionsOverlappingRequestsIntoOneBusyInterval(t *testing.T) {
	busy := mergeIntervals([]Interval{{StartMs: 4200, EndMs: 8300}, {StartMs: 5100, EndMs: 9400}}, 0)
	want := []Interval{{StartMs: 4200, EndMs: 9400}}
	if len(busy) != 1 || busy[0] != want[0] {
		t.Errorf("busy = %+v, want %+v", busy, want)
	}
}

func TestNetworkIntervalsKeepsClearlySeparateRequestsApart(t *testing.T) {
	busy := mergeIntervals([]Interval{{StartMs: 0, EndMs: 1000}, {StartMs: 4000, EndMs: 5000}}, testConfig.Network.MergeGapMs)
	if len(busy) != 2 {
		t.Errorf("len(busy) = %d, want 2", len(busy))
	}
}

func TestNetworkIntervalsOnlyCountsApplicationTraffic(t *testing.T) {
	kept := relevantRequests([]NetworkRecord{
		networkRequest(500, "xmlhttprequest"),
		networkRequest(500, "image"),
		networkRequest(500, "websocket"),
		networkRequest(500, "main_frame"),
	}, testConfig.Network)
	want := []string{"xmlhttprequest", "main_frame"}
	if len(kept) != len(want) {
		t.Fatalf("len = %d, want %d", len(kept), len(want))
	}
	for i, r := range kept {
		if r.Type != want[i] {
			t.Errorf("kept[%d].Type = %q, want %q", i, r.Type, want[i])
		}
	}
}
