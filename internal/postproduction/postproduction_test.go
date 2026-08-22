package postproduction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"take5/internal/director"
	"take5/internal/plate"
	"take5/internal/render"
	"take5/internal/session"
)

func writeMinimalSession(t *testing.T, dir string) {
	t.Helper()
	doc := `{"version":2,"sessionId":"s1","startedAtEpochMs":1000,"durationMs":1500,"url":"https://example.com","viewport":{"width":100,"height":100,"devicePixelRatio":1},"capture":{},"events":[],"network":[]}`
	if err := os.WriteFile(filepath.Join(dir, SessionFile), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Status ---

func TestStatusReady(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, OutputFile), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, reason := Status(dir)
	if status != "ready" {
		t.Errorf("status = %q, want ready", status)
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty", reason)
	}
}

func TestStatusRenderFailed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DebugLogFile), []byte("some log line\nrender: ffmpeg exited 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, reason := Status(dir)
	if status != "render_failed" {
		t.Errorf("status = %q, want render_failed", status)
	}
	if reason != "ffmpeg exited 1" {
		t.Errorf("reason = %q, want %q", reason, "ffmpeg exited 1")
	}
}

// Regression: the render CLI command's ffmpeg-not-found check writes straight to stderr with
// no timestamp prefix, unlike a detached-spawn failure's later-added lines — both must still
// be recognized.
func TestStatusRenderFailedFfmpegMissing(t *testing.T) {
	dir := t.TempDir()
	logLine := "render: ffmpeg was not found. Install it (e.g. `brew install ffmpeg`) and try again.\n"
	if err := os.WriteFile(filepath.Join(dir, DebugLogFile), []byte(logLine), 0o644); err != nil {
		t.Fatal(err)
	}
	status, reason := Status(dir)
	if status != "render_failed" {
		t.Errorf("status = %q, want render_failed", status)
	}
	if reason == "" {
		t.Error("reason is empty, want the ffmpeg-not-found message")
	}
}

// Regression: a detached-spawn failure writes the render marker after a timestamp, not at the
// start of the line.
func TestStatusRenderFailedAfterTimestamp(t *testing.T) {
	dir := t.TempDir()
	logLine := "2026-01-01T00:00:00Z render: could not start post-production: resolve own executable: no such file\n"
	if err := os.WriteFile(filepath.Join(dir, DebugLogFile), []byte(logLine), 0o644); err != nil {
		t.Fatal(err)
	}
	status, reason := Status(dir)
	if status != "render_failed" {
		t.Errorf("status = %q, want render_failed", status)
	}
	if reason != "could not start post-production: resolve own executable: no such file" {
		t.Errorf("reason = %q", reason)
	}
}

func TestStatusRecordingFailed(t *testing.T) {
	dir := t.TempDir()
	logLine := "2026-01-01T00:00:00Z aborted: connection closed before session-stop\n"
	if err := os.WriteFile(filepath.Join(dir, DebugLogFile), []byte(logLine), 0o644); err != nil {
		t.Fatal(err)
	}
	status, reason := Status(dir)
	if status != "recording_failed" {
		t.Errorf("status = %q, want recording_failed", status)
	}
	if reason != "connection closed before session-stop" {
		t.Errorf("reason = %q", reason)
	}
}

func TestStatusProcessingByDefault(t *testing.T) {
	dir := t.TempDir()
	status, _ := Status(dir)
	if status != "processing" {
		t.Errorf("status = %q, want processing", status)
	}
}

// --- Analyze ---

func TestAnalyzeWritesProjectJSON(t *testing.T) {
	dir := t.TempDir()
	writeMinimalSession(t, dir)

	project, err := Analyze(dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(project.Voice) != 0 {
		t.Errorf("Voice = %v, want none (no voice.json)", project.Voice)
	}
	if _, err := os.Stat(filepath.Join(dir, ProjectFile)); err != nil {
		t.Errorf("%s was not written: %v", ProjectFile, err)
	}
}

// TestAnalyzeReadsVoiceJSONWithoutError covers Analyze's own responsibility — merging
// voice.json's cues into the session before handing it to the Director — not whether the
// Director then pairs/places them (director's own tests already cover that, and doing so
// here would need matching recorded actions to pair against).
func TestAnalyzeReadsVoiceJSONWithoutError(t *testing.T) {
	dir := t.TempDir()
	writeMinimalSession(t, dir)
	rewritten := "hello"
	cues := []director.VoiceCue{{ID: "cue-1", SourceStartMs: 0, SourceEndMs: 100, RewrittenText: &rewritten}}
	buf, err := json.Marshal(cues)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, VoiceCuesFile), buf, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Analyze(dir); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
}

func TestAnalyzeMissingSessionJSONFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := Analyze(dir); err == nil {
		t.Fatal("Analyze: want error for missing session.json, got nil")
	}
}

func TestAnalyzeCorruptVoiceJSONFails(t *testing.T) {
	dir := t.TempDir()
	writeMinimalSession(t, dir)
	if err := os.WriteFile(filepath.Join(dir, VoiceCuesFile), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(dir); err == nil {
		t.Fatal("Analyze: want error for corrupt voice.json, got nil")
	}
}

// --- Render: orchestration around the voice stage, without needing ffmpeg ---

func TestRenderErrorsWhenNoRawAndNoFrames(t *testing.T) {
	dir := t.TempDir()
	writeMinimalSession(t, dir)
	if _, err := Render(context.Background(), dir, Options{}); err == nil {
		t.Fatal("Render: want error when neither raw.mp4 nor frames.json exist, got nil")
	}
}

func spyVoiceStage(ran bool, err error, called *bool) VoiceStage {
	return func(context.Context, string) (bool, error) {
		*called = true
		return ran, err
	}
}

func TestRunVoiceStageSkipsWhenNoVoiceWebm(t *testing.T) {
	dir := t.TempDir()
	var called bool
	opts := Options{Voice: spyVoiceStage(true, nil, &called)}
	if opts.runVoiceStage(context.Background(), dir) {
		t.Error("runVoiceStage = true, want false (no voice.webm)")
	}
	if called {
		t.Error("Voice was called despite no voice.webm")
	}
}

func TestRunVoiceStageSkipsWhenVoiceJSONAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.VoiceFile), []byte("fake webm"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, VoiceCuesFile), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	var called bool
	opts := Options{Voice: spyVoiceStage(true, nil, &called)}
	if opts.runVoiceStage(context.Background(), dir) {
		t.Error("runVoiceStage = true, want false (voice.json already exists)")
	}
	if called {
		t.Error("Voice was called despite voice.json already existing")
	}
}

func TestRunVoiceStageCallsVoiceAndReportsRan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.VoiceFile), []byte("fake webm"), 0o644); err != nil {
		t.Fatal(err)
	}
	var called bool
	opts := Options{Voice: spyVoiceStage(true, nil, &called)}
	if !opts.runVoiceStage(context.Background(), dir) {
		t.Error("runVoiceStage = false, want true")
	}
	if !called {
		t.Error("Voice was not called despite voice.webm waiting")
	}
}

func TestRunVoiceStageIsNonFatalOnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.VoiceFile), []byte("fake webm"), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged string
	opts := Options{
		Voice: spyVoiceStage(false, errors.New("transcription failed"), new(bool)),
		Logf:  func(format string, a ...any) { logged = fmt.Sprintf(format, a...) },
	}
	if opts.runVoiceStage(context.Background(), dir) {
		t.Error("runVoiceStage = true, want false on error")
	}
	if logged == "" {
		t.Error("Voice's error was not logged")
	}
}

func TestRunVoiceStageNilVoiceIsANoOp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.VoiceFile), []byte("fake webm"), 0o644); err != nil {
		t.Fatal(err)
	}
	var opts Options
	if opts.runVoiceStage(context.Background(), dir) {
		t.Error("runVoiceStage = true, want false when Voice is nil")
	}
}

func TestProjectPredatesVoiceJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, VoiceCuesFile), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(dir, ProjectFile), now, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, VoiceCuesFile), now, now); err != nil {
		t.Fatal(err)
	}
	if !projectPredatesVoiceJSON(dir) {
		t.Error("projectPredatesVoiceJSON = false, want true (project.json older)")
	}

	if err := os.Chtimes(filepath.Join(dir, ProjectFile), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if projectPredatesVoiceJSON(dir) {
		t.Error("projectPredatesVoiceJSON = true, want false (project.json newer)")
	}
}

func TestProjectPredatesVoiceJSONFalseWhenEitherFileMissing(t *testing.T) {
	dir := t.TempDir()
	if projectPredatesVoiceJSON(dir) {
		t.Error("projectPredatesVoiceJSON = true, want false when both files are missing")
	}
}

// --- Render: full pipeline, needs a real ffmpeg to produce demo.mp4 ---

func TestRenderEndToEndWithExistingPlateSkipsAssembly(t *testing.T) {
	if !render.IsFfmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	writeMinimalSession(t, dir)
	if err := render.RunFfmpeg([]string{
		"-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-an",
		filepath.Join(dir, plate.RawFile),
	}, ""); err != nil {
		t.Fatalf("writeRawVideo: %v", err)
	}

	result, err := Render(context.Background(), dir, Options{Logf: t.Logf})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result.Output == "" {
		t.Error("Result.Output is empty")
	}
	if _, err := os.Stat(filepath.Join(dir, OutputFile)); err != nil {
		t.Errorf("%s was not produced: %v", OutputFile, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ProjectFile)); err != nil {
		t.Errorf("%s was not written: %v", ProjectFile, err)
	}
}

// TestRenderSweepsStaleFramesAlongsideAnAlreadyValidRaw covers the shape a crash between
// plate.assembleFromSegments' own raw.mp4 rename and its frames/segments cleanup — or a
// session left over from before this feature — would have: an already-valid raw.mp4 with
// stale frames/ still sitting next to it. plate.EnsureRaw's fast path sweeps them even when it
// doesn't need to rebuild anything, so a stale session doesn't keep its stray sources forever.
func TestRenderSweepsStaleFramesAlongsideAnAlreadyValidRaw(t *testing.T) {
	if !render.IsFfmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	writeMinimalSession(t, dir)
	if err := render.RunFfmpeg([]string{
		"-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-an",
		filepath.Join(dir, plate.RawFile),
	}, ""); err != nil {
		t.Fatalf("writeRawVideo: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "frames"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frames", "000001-000000000000.jpg"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Render(context.Background(), dir, Options{Logf: t.Logf}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "frames")); !os.IsNotExist(err) {
		t.Errorf("frames/ still exists after Render (err=%v)", err)
	}
}
