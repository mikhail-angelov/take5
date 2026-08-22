package director

import (
	"strings"
	"testing"
)

// Port of test/annotations.test.js.

func shortcutEvent(t float64, key string) Event {
	return Event{Kind: "shortcut", T: t, Key: key}
}

func planForAnnotations(events []Event) []Annotation {
	actions := meaningfulActions(events)
	units := computePairableUnits(actions)
	timeline := buildTimeline(planSegments(units, 20000, nil, testConfig).Segments)
	return planAnnotations(actions, timeline, testViewport, testConfig)
}

func TestPlanAnnotationsShowsABadgeForAKeyboardShortcut(t *testing.T) {
	annotations := planForAnnotations([]Event{clickEvent(1000, 10, 10), shortcutEvent(3000, "Meta+K")})
	var badge *Annotation
	for i, a := range annotations {
		if a.Kind == "shortcut" {
			badge = &annotations[i]
		}
	}
	if badge == nil {
		t.Fatal("no shortcut badge found")
	}
	if badge.Text != "Meta+K" {
		t.Errorf("text = %q, want Meta+K", badge.Text)
	}
	if badge.EndMs-badge.StartMs != testConfig.Annotations.ShortcutDurationMs {
		t.Errorf("duration = %v, want %v", badge.EndMs-badge.StartMs, testConfig.Annotations.ShortcutDurationMs)
	}
}

func TestPlanAnnotationsLabelsASmallIconLikeTarget(t *testing.T) {
	annotations := planForAnnotations([]Event{
		clickEventRect(1000, 1216, 56, Rect{X: 1200, Y: 40, Width: 32, Height: 32}, "Settings"),
	})
	if len(annotations) != 1 {
		t.Fatalf("len = %d, want 1", len(annotations))
	}
	if annotations[0].Kind != "label" || annotations[0].Text != "Settings" {
		t.Errorf("got %+v", annotations[0])
	}
}

func TestPlanAnnotationsLeavesLargeObviousControlsToCursorAndZoom(t *testing.T) {
	annotations := planForAnnotations([]Event{
		clickEventRect(1000, 700, 450, Rect{X: 200, Y: 300, Width: 900, Height: 300}, "Generate"),
	})
	if len(annotations) != 0 {
		t.Errorf("annotations = %+v, want empty", annotations)
	}
}

func TestPlanAnnotationsSaysNothingAboutATargetWithNoLabel(t *testing.T) {
	e := clickEvent(1000, 20, 20)
	e.Target = &Target{Rect: &Rect{X: 0, Y: 0, Width: 40, Height: 40}}
	annotations := planForAnnotations([]Event{e})
	if len(annotations) != 0 {
		t.Errorf("annotations = %+v, want empty", annotations)
	}
}

func TestPlanAnnotationsCollapsesARunOfTypingIntoOneCaption(t *testing.T) {
	fieldRect := Rect{X: 420, Y: 240, Width: 200, Height: 42}
	annotations := planForAnnotations([]Event{
		inputEventRect(1000, "Project name", fieldRect),
		inputEventRect(1200, "Project name", fieldRect),
		inputEventRect(1400, "Project name", fieldRect),
		inputEventRect(1600, "Project name", fieldRect),
	})
	if len(annotations) != 1 {
		t.Fatalf("len = %d, want 1", len(annotations))
	}
	if annotations[0].Text != "Project name" {
		t.Errorf("text = %q, want Project name", annotations[0].Text)
	}
}

func TestPlanAnnotationsNeverShowsTwoCaptionsAtOnce(t *testing.T) {
	annotations := planForAnnotations([]Event{
		clickEventRect(1000, 30, 30, Rect{X: 10, Y: 10, Width: 40, Height: 40}, "First"),
		clickEventRect(1600, 130, 30, Rect{X: 110, Y: 10, Width: 40, Height: 40}, "Second"),
		clickEventRect(2200, 230, 30, Rect{X: 210, Y: 10, Width: 40, Height: 40}, "Third"),
	})
	if len(annotations) != 3 {
		t.Fatalf("len = %d, want 3", len(annotations))
	}
	for i := 1; i < len(annotations); i++ {
		if annotations[i-1].EndMs > annotations[i].StartMs {
			t.Errorf("annotation %d overlaps annotation %d", i-1, i)
		}
	}
}

func TestPlanAnnotationsTruncatesALongLabelInsteadOfWritingASentence(t *testing.T) {
	long := "Create a brand new project from the currently selected template"
	annotations := planForAnnotations([]Event{
		clickEventRect(1000, 30, 30, Rect{X: 10, Y: 10, Width: 40, Height: 40}, long),
	})
	if len(annotations) == 0 {
		t.Fatal("expected an annotation")
	}
	if len([]rune(annotations[0].Text)) != testConfig.Annotations.MaxTextLength {
		t.Errorf("len(text) = %d, want %d", len([]rune(annotations[0].Text)), testConfig.Annotations.MaxTextLength)
	}
	if !strings.HasSuffix(annotations[0].Text, "…") {
		t.Errorf("text = %q, want to end with …", annotations[0].Text)
	}
}

// TestPlanAnnotationsNeedsNoCodeChangeForAFreeze locks in
// docs/plans/2026-08-21-voice-sync-and-freeze-frame.md Task 4's claim: planAnnotations only
// ever calls sourceTimeToOutputTime on action instants, so a held segment inserted between
// two actions shifts the SECOND action's annotation by exactly HoldMs and leaves the first
// one untouched, with zero changes to annotations.go itself.
func TestPlanAnnotationsNeedsNoCodeChangeForAFreeze(t *testing.T) {
	actions := []Action{
		{Kind: "shortcut", StartMs: 500, EndMs: 500, Event: Event{Kind: "shortcut", Key: "Meta+S"}},
		{Kind: "shortcut", StartMs: 2000, EndMs: 2000, Event: Event{Kind: "shortcut", Key: "Meta+K"}},
	}
	noHold := buildTimeline([]RawSegment{{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1}})
	withHold := buildTimeline([]RawSegment{
		{SourceStartMs: 0, SourceEndMs: 3000, Speed: 1},
		{SourceStartMs: 1000, SourceEndMs: 1000, HoldMs: 500},
	})

	before := planAnnotations(actions, noHold, testViewport, testConfig)
	after := planAnnotations(actions, withHold, testViewport, testConfig)
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("before = %#v, after = %#v, want 2 annotations each", before, after)
	}
	if before[0].StartMs != after[0].StartMs {
		t.Errorf("annotation before the freeze: StartMs = %v, want unchanged at %v", after[0].StartMs, before[0].StartMs)
	}
	if got := after[1].StartMs - before[1].StartMs; got != 500 {
		t.Errorf("annotation after the freeze shifted by %v, want exactly 500 (the HoldMs)", got)
	}
}
