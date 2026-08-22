package host

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeSessionDir(t *testing.T, outputDir, name, sessionID string, startedAtEpochMs float64) string {
	t.Helper()
	dir := filepath.Join(outputDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := fmt.Sprintf(
		`{"version":2,"sessionId":%q,"startedAtEpochMs":%d,"durationMs":1500,"url":"https://example.com","viewport":{"width":100,"height":100,"devicePixelRatio":1},"capture":{},"events":[],"network":[]}`,
		sessionID, int64(startedAtEpochMs),
	)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Status classification detail (markers, timestamp handling) is characterized directly against
// internal/postproduction.Status, the seam that now owns it — see
// TestStatus* in internal/postproduction/postproduction_test.go. The tests below cover only
// what ListSessions/summarizeSession add on top: reading session.json and assembling
// SessionSummary from it.

func TestListSessionsOrdersMostRecentFirstAndSkipsJunk(t *testing.T) {
	outputDir := t.TempDir()
	writeSessionDir(t, outputDir, "2026-01-01-000000", "older", 1000)
	writeSessionDir(t, outputDir, "2026-01-02-000000", "newer", 2000)
	if err := os.MkdirAll(filepath.Join(outputDir, "not-a-session"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	summaries, err := ListSessions(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}
	if summaries[0].ID != "newer" || summaries[1].ID != "older" {
		t.Errorf("order = [%s, %s], want [newer, older]", summaries[0].ID, summaries[1].ID)
	}
}

// TestListSessionsWiresStatusAndOutputFromPostproduction covers the seam itself — that
// summarizeSession calls postproduction.Status and sets Output from its result — not the
// classification rules, which belong to postproduction's own tests.
func TestListSessionsWiresStatusAndOutputFromPostproduction(t *testing.T) {
	outputDir := t.TempDir()
	sessionDir := writeSessionDir(t, outputDir, "2026-01-01-000000", "s1", 1000)
	if err := os.WriteFile(filepath.Join(sessionDir, "demo.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	summaries, err := ListSessions(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if summaries[0].Status != "ready" {
		t.Errorf("status = %q, want ready", summaries[0].Status)
	}
	if want := filepath.Join(sessionDir, "demo.mp4"); summaries[0].Output != want {
		t.Errorf("output = %q, want %q", summaries[0].Output, want)
	}
}

func TestListSessionsMissingOutputDirIsEmpty(t *testing.T) {
	summaries, err := ListSessions(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Errorf("len(summaries) = %d, want 0", len(summaries))
	}
}
