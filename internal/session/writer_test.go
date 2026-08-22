package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startInfo() StartInfo {
	return StartInfo{
		SessionID:        "s1",
		StartedAtEpochMs: 1000,
		URL:              "https://example.com",
		Viewport:         Viewport{Width: 100, Height: 100, DevicePixelRatio: 1},
		Capture:          map[string]any{},
	}
}

func readSessionJSON(t *testing.T, dir string) Document {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		t.Fatalf("session.json was not written: %v", err)
	}
	var doc Document
	if err := json.Unmarshal(buf, &doc); err != nil {
		t.Fatalf("session.json is not valid JSON: %v", err)
	}
	return doc
}

// TestOpenCheckpointsSessionJSONImmediately locks in the fix for a lost recording: a session
// killed before its first event or Abort/Finalize used to leave zero trace on disk. Open now
// writes what it already knows (sessionId/url/viewport) right away.
func TestOpenCheckpointsSessionJSONImmediately(t *testing.T) {
	w, err := Open(t.TempDir(), startInfo())
	if err != nil {
		t.Fatal(err)
	}
	doc := readSessionJSON(t, w.Dir)
	if doc.SessionID != "s1" || doc.URL != "https://example.com" {
		t.Errorf("doc = %+v, want the StartInfo fields", doc)
	}
	if doc.Events == nil || len(doc.Events) != 0 {
		t.Errorf("Events = %v, want an empty (not nil) slice", doc.Events)
	}
	if doc.Network == nil || len(doc.Network) != 0 {
		t.Errorf("Network = %v, want an empty (not nil) slice", doc.Network)
	}
}

// readJournalLines is a small test-only reader — separate from the package's own readJournal,
// which returns parsed events/network, not raw lines — used here just to check what actually
// landed on disk line-by-line.
func readJournalLines(t *testing.T, dir string) []string {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(dir, JournalFile))
	if err != nil {
		t.Fatalf("%s was not written: %v", JournalFile, err)
	}
	text := strings.TrimRight(string(buf), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// TestAddEventAndAddNetworkRecordAppendToJournalWithoutRewritingSessionJSON is the direct
// regression test for the production incident (a hard-killed process never calls Finalize or
// Abort) combined with the architecture question that followed it: durability for events/
// network must not come from rewriting the whole, growing session.json on every single one —
// JournalFile is appended to instead, in O(1) per call, and session.json itself is left as
// Open first wrote it (empty events/network) until Finalize/Abort's real merge-and-sort runs.
func TestAddEventAndAddNetworkRecordAppendToJournalWithoutRewritingSessionJSON(t *testing.T) {
	w, err := Open(t.TempDir(), startInfo())
	if err != nil {
		t.Fatal(err)
	}
	w.AddEvent(map[string]any{"kind": "click", "t": float64(500)})
	w.AddNetworkRecord(NetworkRecord{ID: "1", StartMs: 100, EndMs: 300, Type: "xmlhttprequest", Status: 200})
	// No Finalize, no Abort: simulates the process being killed right here.

	lines := readJournalLines(t, w.Dir)
	if len(lines) != 2 {
		t.Fatalf("journal has %d lines, want 2 (one per Add* call)", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "event" {
		t.Errorf("first journal line type = %v, want event", first["type"])
	}

	// session.json itself is untouched since Open's own checkpoint — that's the point: no
	// per-event rewrite of the whole document.
	doc := readSessionJSON(t, w.Dir)
	if len(doc.Events) != 0 || len(doc.Network) != 0 {
		t.Errorf("session.json Events/Network = %v/%v, want both still empty (durability is the journal's job now)", doc.Events, doc.Network)
	}
}

// TestRecoverHealsSessionJSONFromJournal is the other half: once the process is dead,
// something has to turn the journal back into the events/network session.json should have had
// all along. This is what internal/postproduction.Render calls before Analyze.
func TestRecoverHealsSessionJSONFromJournal(t *testing.T) {
	w, err := Open(t.TempDir(), startInfo())
	if err != nil {
		t.Fatal(err)
	}
	w.AddEvent(map[string]any{"kind": "click", "t": float64(500)})
	w.AddNetworkRecord(NetworkRecord{ID: "1", StartMs: 100, EndMs: 300, Type: "xmlhttprequest", Status: 200})
	dir := w.Dir

	healed, err := Recover(dir)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !healed {
		t.Fatal("Recover reported no change, want it to have healed session.json from the journal")
	}

	doc := readSessionJSON(t, dir)
	if len(doc.Events) != 1 || doc.Events[0]["kind"] != "click" {
		t.Errorf("Events = %v, want the click event recovered from the journal", doc.Events)
	}
	if len(doc.Network) != 1 || doc.Network[0].ID != "1" {
		t.Errorf("Network = %v, want the network record recovered from the journal", doc.Network)
	}
	if doc.DurationMs != 500 {
		t.Errorf("DurationMs = %d, want 500 (implied from the recovered event's t)", doc.DurationMs)
	}

	// A second Recover on an already-healed session.json must be a no-op — it now has real
	// events/network, so the journal (a fallback source) is never consulted again.
	healedAgain, err := Recover(dir)
	if err != nil {
		t.Fatalf("Recover (second call): %v", err)
	}
	if healedAgain {
		t.Error("Recover healed an already-healed session.json; it should have been a no-op")
	}
}

// TestRecoverLeavesACompletedSessionUntouched guards the "never override Finalize/Abort's own
// write" half of Recover's contract, independent of whether a journal happens to exist too.
func TestRecoverLeavesACompletedSessionUntouched(t *testing.T) {
	w, err := Open(t.TempDir(), startInfo())
	if err != nil {
		t.Fatal(err)
	}
	w.AddEvent(map[string]any{"kind": "click", "t": float64(500)})
	if _, err = w.Finalize(2500); err != nil {
		t.Fatal(err)
	}
	before := readSessionJSON(t, w.Dir)

	healed, err := Recover(w.Dir)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if healed {
		t.Error("Recover changed a session.json that already had real events; it must not")
	}
	after := readSessionJSON(t, w.Dir)
	if after.DurationMs != before.DurationMs || len(after.Events) != len(before.Events) {
		t.Errorf("session.json changed: before=%+v after=%+v", before, after)
	}
}
