package render

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"take5/internal/director"
)

// Phase 3 gate 2 of docs/plans/go-port.md: an end-to-end run must produce a demo.mp4 with
// the same duration and frame count as the Node oracle. Gate 1 (golden_test.go) already
// proves the two implementations feed FFmpeg character-identical filter graphs and ASS
// files, so this test's job is to prove the Go orchestration around FFmpeg — probing,
// writing intermediates in the right order, invoking both passes — actually produces a
// working video, and to check that claim against a real run of the Node pipeline on the
// same inputs rather than just asserting "Go didn't crash".
//
// The session below mirrors test/render.test.js's SESSION (dead air, a compressed network
// wait, a camera shot, an annotation, a shortcut) so this exercises the same shape of demo.

const (
	rawWidth  = 640
	rawHeight = 360
	rawFPS    = 30
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

func writeRawVideo(t *testing.T, path string, durationSec int) {
	t.Helper()
	err := RunFfmpeg([]string{
		"-y",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d:duration=%d", rawWidth, rawHeight, rawFPS, durationSec),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-an",
		path,
	}, "")
	if err != nil {
		t.Fatalf("writeRawVideo: %v", err)
	}
}

func endToEndSession() director.Session {
	target := func(role, label string, r director.Rect) *director.Target {
		return &director.Target{Role: role, Label: label, Rect: &r}
	}
	f := func(v float64) *float64 { return &v }
	return director.Session{
		Version:          2,
		SessionID:        "render-e2e",
		StartedAtEpochMs: 1786957200123,
		DurationMs:       12000,
		URL:              "https://app.example.com/",
		Viewport:         director.Viewport{Width: 1280, Height: 720, DevicePixelRatio: 1},
		Capture:          map[string]any{},
		Events: []director.Event{
			{Kind: "click", T: 800, X: f(610), Y: f(350), Target: target("button", "Generate", director.Rect{X: 160, Y: 200, Width: 900, Height: 300})},
			{Kind: "click", T: 8800, X: f(1216), Y: f(56), Target: target("button", "Settings", director.Rect{X: 1200, Y: 40, Width: 32, Height: 32})},
			{Kind: "input", T: 9400, Target: target("textbox", "Project name", director.Rect{X: 400, Y: 300, Width: 300, Height: 40})},
			{Kind: "shortcut", T: 10200, Key: "Meta+Enter"},
			{Kind: "click", T: 11000, X: f(1045), Y: f(618), Target: target("button", "Save", director.Rect{X: 1000, Y: 600, Width: 90, Height: 36})},
		},
		Network: []director.NetworkRecord{{ID: "1", StartMs: 900, EndMs: 7200, Type: "xmlhttprequest", Status: 200}},
	}
}

func TestEndToEndRenderProducesAWorkingDemo(t *testing.T) {
	if !IsFfmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}

	session := endToEndSession()
	sessionBuf, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}

	goDir := t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(goDir, "session.json"), sessionBuf, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	writeRawVideo(t, filepath.Join(goDir, "raw.mp4"), 12)

	project := director.Direct(session)
	if project.DurationMs >= 7000 {
		t.Fatalf("expected a much shorter demo, got %d ms", project.DurationMs)
	}
	if project.DurationMs <= 3000 {
		t.Fatalf("expected the story to survive, got %d ms", project.DurationMs)
	}
	if len(project.Camera) == 0 {
		t.Error("the small Settings icon should get a camera shot")
	}

	result, err := Project(goDir, project, t.Logf)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(goDir, "demo.mp4")); statErr != nil {
		t.Fatalf("demo.mp4 was not produced: %v", statErr)
	}
	if result.Width != rawWidth || result.Height != rawHeight {
		t.Errorf("dimensions = %dx%d, want %dx%d", result.Width, result.Height, rawWidth, rawHeight)
	}
	if result.DurationMs == nil {
		t.Fatal("probe reported no duration")
	}
	stdout, err := runWithOutput(FfprobeBin(), []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=nw=1",
		filepath.Join(goDir, "demo.mp4"),
	}, "")
	if err != nil {
		t.Fatalf("probing audio stream: %v", err)
	}
	if !strings.Contains(stdout, "codec_name=aac") {
		t.Errorf("audio stream = %q, want AAC", stdout)
	}
	diff := *result.DurationMs - project.DurationMs
	if diff < 0 {
		diff = -diff
	}
	if diff >= 500 {
		t.Errorf("rendered %d ms but planned %d ms", *result.DurationMs, project.DurationMs)
	}

	// Intermediates are cleaned up after a successful render (spec 10).
	if _, statErr := os.Stat(filepath.Join(goDir, ".tmp")); !os.IsNotExist(statErr) {
		t.Error(".tmp should have been removed after a successful render")
	}

	nodeCLI := filepath.Join(repoRoot(t), "src", "cli.js")
	if _, lookErr := exec.LookPath("node"); lookErr != nil {
		t.Skip("node is not on PATH; skipping the cross-implementation comparison")
	}
	if _, statErr := os.Stat(nodeCLI); statErr != nil {
		t.Skip("src/cli.js not found; skipping the cross-implementation comparison")
	}

	nodeDir := t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(nodeDir, "session.json"), sessionBuf, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	writeRawVideo(t, filepath.Join(nodeDir, "raw.mp4"), 12)

	if out, cmdErr := exec.Command("node", nodeCLI, "render", nodeDir).CombinedOutput(); cmdErr != nil {
		t.Fatalf("node render failed: %v\n%s", cmdErr, out)
	}

	nodeProbe, err := ProbeVideo(filepath.Join(nodeDir, "demo.mp4"))
	if err != nil {
		t.Fatalf("probing the Node-rendered demo.mp4: %v", err)
	}
	goProbe, err := ProbeVideo(filepath.Join(goDir, "demo.mp4"))
	if err != nil {
		t.Fatalf("probing the Go-rendered demo.mp4: %v", err)
	}

	if goProbe.Width != nodeProbe.Width || goProbe.Height != nodeProbe.Height {
		t.Errorf("dimensions differ: go=%dx%d node=%dx%d", goProbe.Width, goProbe.Height, nodeProbe.Width, nodeProbe.Height)
	}
	if goProbe.DurationMs == nil || nodeProbe.DurationMs == nil {
		t.Fatal("one of the two renders reported no duration")
	}
	durationDiff := *goProbe.DurationMs - *nodeProbe.DurationMs
	if durationDiff < 0 {
		durationDiff = -durationDiff
	}
	// Same fps_mode=cfr plan and character-identical filter graphs (golden_test.go); any
	// gap here would mean the two orchestrations disagree about something gate 1 can't see.
	if durationDiff > 34 { // just over one frame at 30fps
		t.Errorf("Go and Node demo.mp4 durations differ by %d ms: go=%d node=%d",
			durationDiff, *goProbe.DurationMs, *nodeProbe.DurationMs)
	}

	goFrames := frameCount(goProbe)
	nodeFrames := frameCount(nodeProbe)
	if goFrames != nodeFrames {
		t.Errorf("frame count differs: go=%d node=%d", goFrames, nodeFrames)
	}
}

func frameCount(p VideoProbe) int64 {
	if p.DurationMs == nil {
		return -1
	}
	return int64(jsMathRound(float64(*p.DurationMs) / 1000 * rawFPS))
}
