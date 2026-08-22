package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"take5/internal/postproduction"
	"take5/internal/session"
)

// SessionSummary is one entry in the list-sessions response. There is no separate index
// file to go stale: everything here is read straight off what render already leaves behind
// in a session directory.
type SessionSummary struct {
	ID               string `json:"id"`
	Dir              string `json:"dir"`
	URL              string `json:"url"`
	StartedAtEpochMs int64  `json:"startedAtEpochMs"`
	DurationMs       int64  `json:"durationMs"`
	Status           string `json:"status"`
	Output           string `json:"output,omitempty"`
	// Reason is set for render_failed and recording_failed: the message text pulled from
	// debug.log, so a popup can show *why* without the user having to go find that file.
	Reason string `json:"reason,omitempty"`
}

// Most recent first; a popup history view has no use for the full lifetime of recordings.
const maxHistoryEntries = 100

// ListSessions scans outputDir for recorded sessions, most recent first.
func ListSessions(outputDir string) ([]SessionSummary, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionSummary{}, nil
		}
		return nil, fmt.Errorf("read output dir: %w", err)
	}

	summaries := make([]SessionSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		summary, ok := summarizeSession(filepath.Join(outputDir, entry.Name()))
		if !ok {
			continue
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].StartedAtEpochMs > summaries[j].StartedAtEpochMs
	})
	if len(summaries) > maxHistoryEntries {
		summaries = summaries[:maxHistoryEntries]
	}
	return summaries, nil
}

func summarizeSession(dir string) (SessionSummary, bool) {
	// dir is one of outputDir's own subdirectories, listed by ListSessions above, not
	// attacker-controlled input.
	buf, err := os.ReadFile(filepath.Join(dir, postproduction.SessionFile)) // #nosec G304
	if err != nil {
		return SessionSummary{}, false
	}
	var doc session.Document
	if err := json.Unmarshal(buf, &doc); err != nil {
		return SessionSummary{}, false
	}

	summary := SessionSummary{
		ID:               doc.SessionID,
		Dir:              dir,
		URL:              doc.URL,
		StartedAtEpochMs: int64(doc.StartedAtEpochMs),
		DurationMs:       doc.DurationMs,
	}
	summary.Status, summary.Reason = postproduction.Status(dir)
	if summary.Status == "ready" {
		summary.Output = filepath.Join(dir, postproduction.OutputFile)
	}
	return summary, true
}
