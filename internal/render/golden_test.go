package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"take5/internal/director"
)

// Phase 3 gate 1 of docs/plans/go-port.md: generated filter graphs and ASS files must
// match the frozen Node oracle character for character — they are what determines the
// picture, so they are what to compare, not the video. video is synthetic here for the
// same reason tools/freeze.mjs's is: devicePixelRatio x viewport, fps fixed at 30.

const goldenFPS = 30

func corpusDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "test", "fixtures", "corpus")
}

func readGolden(t *testing.T, dir, name string) (string, bool) {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(buf), true
}

// goldenTemporalJob is one Pass A job's own FFmpeg argv, serialized for a byte-exact golden
// comparison — the "clean function -> golden file" discipline this file has always used,
// applied to PlanTemporalJobs/TemporalSegmentArgs' per-segment output instead of the single
// filter_complex BuildTemporalFilterGraph used to produce. outPath is a fixed, index-derived
// placeholder rather than a real temp path, so the golden file is deterministic across runs.
type goldenTemporalJob struct {
	Args []string `json:"args"`
}

func buildGoldenTemporalJobs(t *testing.T, project director.Project, fps int) string {
	t.Helper()
	jobs, err := PlanTemporalJobs(project.Timeline)
	if err != nil {
		t.Fatalf("PlanTemporalJobs: %v", err)
	}
	golden := make([]goldenTemporalJob, len(jobs))
	for i, job := range jobs {
		outPath := fmt.Sprintf("%06d.mp4", job.Index)
		golden[i] = goldenTemporalJob{Args: TemporalSegmentArgs(project.Source, job, fps, outPath)}
	}
	buf, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden temporal jobs: %v", err)
	}
	return string(buf)
}

func loadSession(t *testing.T, path string) director.Session {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var session director.Session
	if err := json.Unmarshal(buf, &session); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestGoldenFiltersAndAssMatchTheFrozenNodeOracle(t *testing.T) {
	base := corpusDir(t)
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(base, name)
			session := loadSession(t, filepath.Join(dir, "session.json"))
			project := director.Direct(session)

			passAJobs := buildGoldenTemporalJobs(t, project, goldenFPS)
			if want, ok := readGolden(t, dir, "pass-a-jobs.json"); ok {
				if got := passAJobs + "\n"; got != want {
					t.Errorf("pass-a-jobs.json mismatch:\n got:  %s\n want: %s", got, want)
				}
			}

			video := VideoProbe{
				Width:  int(session.Viewport.Width * session.Viewport.DevicePixelRatio),
				Height: int(session.Viewport.Height * session.Viewport.DevicePixelRatio),
			}

			hasOverlay := project.Cursor.Start != nil || len(project.Cursor.Clicks) > 0 || len(project.Annotations) > 0
			assFile := ""
			if hasOverlay {
				ass := BuildAss(project, video, goldenFPS)
				if want, ok := readGolden(t, dir, "overlay.ass"); ok {
					if got := ass + "\n"; got != want {
						t.Errorf("overlay.ass mismatch:\n got:  %q\n want: %q", got, want)
					}
				}
				assFile = "overlay.ass"
			} else if _, ok := readGolden(t, dir, "overlay.ass"); ok {
				t.Error("expected no overlay.ass golden for a project with no overlay")
			}

			passB := BuildVisualFilterGraph(project.Camera, project.Viewport, video, goldenFPS, assFile)
			if want, ok := readGolden(t, dir, "pass-b.filter"); ok {
				if got := passB + "\n"; got != want {
					t.Errorf("pass-b.filter mismatch:\n got:  %q\n want: %q", got, want)
				}
			} else if passB != "" {
				t.Errorf("produced a pass-b.filter but no golden exists: %q", passB)
			}
		})
	}
}
