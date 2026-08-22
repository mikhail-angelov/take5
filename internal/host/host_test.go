package host

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"take5/internal/plate"
	"take5/internal/render"
)

func frame(t *testing.T, v map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	return append(lenBuf[:], payload...)
}

func TestRunRecordsACompleteSession(t *testing.T) {
	dir := t.TempDir()
	jpegBytes := []byte{0xff, 0xd8, 0xff, 0xd9}

	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "session-start", "protocolVersion": float64(ProtocolVersion),
		"sessionId": "s1", "startedAtEpochMs": float64(1000),
		"url":      "https://example.com",
		"viewport": map[string]any{"width": float64(1440), "height": float64(900), "devicePixelRatio": float64(2)},
	}))
	in.Write(frame(t, map[string]any{
		"type": "frame", "tMs": float64(0), "data": base64.StdEncoding.EncodeToString(jpegBytes),
	}))
	in.Write(frame(t, map[string]any{
		"type": "event", "event": map[string]any{"kind": "click", "t": float64(100), "x": float64(10), "y": float64(20)},
	}))
	in.Write(frame(t, map[string]any{
		"type": "network", "record": map[string]any{"id": "1", "startMs": float64(50), "endMs": float64(150), "type": "xmlhttprequest", "status": float64(200)},
	}))
	in.Write(frame(t, map[string]any{"type": "session-stop", "endedAtEpochMs": float64(1500)}))

	var out bytes.Buffer
	var completedDir string
	opts := Options{
		OutputDir:  dir,
		OnComplete: func(d string) { completedDir = d },
		OnFailed:   func(d, reason string) { t.Fatalf("unexpected failure: %s (%s)", reason, d) },
	}
	if err := Run(&in, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if completedDir == "" {
		t.Fatal("OnComplete was never called")
	}

	sessionBuf, err := os.ReadFile(filepath.Join(completedDir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if unmarshalErr := json.Unmarshal(sessionBuf, &doc); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if doc["durationMs"].(float64) != 500 {
		t.Errorf("durationMs = %v, want 500", doc["durationMs"])
	}
	events := doc["events"].([]any)
	if len(events) != 1 {
		t.Errorf("len(events) = %d, want 1", len(events))
	}
	network := doc["network"].([]any)
	if len(network) != 1 {
		t.Errorf("len(network) = %d, want 1", len(network))
	}

	// The fake JPEG isn't a decodable image, so internal/plate's encode fails and this frame
	// is never committed into a segment — its file survives on disk as recovery material,
	// exactly as a real encode failure would leave it. session.json finalizing regardless is
	// the point: a plate hiccup must not turn a completed recording into recording_failed.
	jpegOnDisk, err := os.ReadFile(filepath.Join(completedDir, "frames", "000001-000000000000.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(jpegOnDisk, jpegBytes) {
		t.Errorf("frame bytes on disk do not match what was sent")
	}

	// One session-accepted response.
	msg, err := ReadMessage(&out)
	if err != nil {
		t.Fatal(err)
	}
	var accepted map[string]any
	_ = json.Unmarshal(msg, &accepted)
	if accepted["type"] != "session-accepted" {
		t.Errorf("first response type = %v, want session-accepted", accepted["type"])
	}

	// Then session-finalized: the extension's stopRecording sends session-stop and
	// disconnects right after with no other way to confirm Finalize actually completed —
	// without this, a host killed mid-Finalize (production incident) leaves the extension
	// showing a success notification for a recording that was never actually saved.
	msg, err = ReadMessage(&out)
	if err != nil {
		t.Fatal(err)
	}
	var finalized map[string]any
	_ = json.Unmarshal(msg, &finalized)
	if finalized["type"] != "session-finalized" {
		t.Errorf("second response type = %v, want session-finalized", finalized["type"])
	}
	if finalized["durationMs"].(float64) != 500 {
		t.Errorf("session-finalized durationMs = %v, want 500", finalized["durationMs"])
	}
}

func TestRunAbortsOnDisconnectBeforeStop(t *testing.T) {
	dir := t.TempDir()
	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "session-start", "protocolVersion": float64(ProtocolVersion),
		"sessionId": "s1", "startedAtEpochMs": float64(1000),
		"viewport": map[string]any{"width": float64(100), "height": float64(100)},
	}))
	// No session-stop: stdin just ends, as if Chrome killed the port.

	var out bytes.Buffer
	var failedReason string
	opts := Options{
		OutputDir: dir,
		OnFailed:  func(d, reason string) { failedReason = reason },
	}
	if err := Run(&in, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if failedReason != "connection closed before session-stop" {
		t.Errorf("failedReason = %q", failedReason)
	}
}

// readThenClosed serves data, then simulates cmd/take5's signal handler closing
// os.Stdin out from under a blocked read: every read past the end of data returns an error
// wrapping os.ErrClosed, the same as a real closed os.File would.
type readThenClosed struct {
	data *bytes.Reader
}

func (r *readThenClosed) Read(p []byte) (int, error) {
	if r.data.Len() > 0 {
		return r.data.Read(p) //nolint:wrapcheck // io.Reader passthrough; wrapping would break errors.Is(err, io.EOF)
	}
	return 0, &fs.PathError{Op: "read", Path: "stdin", Err: os.ErrClosed}
}

// TestRunAbortsWhenReaderIsClosedMidSession locks in the fix for a lost recording: no
// session.json, no "aborted:" line in debug.log at all, evidence the host process was killed
// (most likely SIGTERM) before Chrome ever closed the port normally. cmd/take5's host
// command now catches that signal and closes stdin itself; Run must treat the resulting
// os.ErrClosed the same as a normal EOF — running Abort and writing session.json — not as a
// hard error that skips finializeSession entirely.
func TestRunAbortsWhenReaderIsClosedMidSession(t *testing.T) {
	dir := t.TempDir()
	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "session-start", "protocolVersion": float64(ProtocolVersion),
		"sessionId": "s1", "startedAtEpochMs": float64(1000),
		"viewport": map[string]any{"width": float64(100), "height": float64(100)},
	}))
	// No session-stop: the reader closes instead, as a signal-triggered shutdown would.
	r := &readThenClosed{data: bytes.NewReader(in.Bytes())}

	var out bytes.Buffer
	var failedReason, completedDir string
	opts := Options{
		OutputDir:  dir,
		OnFailed:   func(d, reason string) { failedReason = reason },
		OnComplete: func(d string) { completedDir = d },
	}
	if err := Run(r, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if failedReason != "connection closed before session-stop" {
		t.Errorf("failedReason = %q", failedReason)
	}
	if completedDir != "" {
		t.Errorf("OnComplete fired for a session that never got session-stop: %q", completedDir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one session directory, got %d", len(entries))
	}
	sessionJSONPath := filepath.Join(dir, entries[0].Name(), "session.json")
	if _, err := os.Stat(sessionJSONPath); err != nil {
		t.Errorf("session.json was not written after the reader closed: %v", err)
	}
}

func TestRunAnswersListSessionsWithoutRequiringASession(t *testing.T) {
	dir := t.TempDir()
	writeSessionDir(t, dir, "2026-01-01-000000", "s1", 1000)

	var in bytes.Buffer
	in.Write(frame(t, map[string]any{"type": "list-sessions"}))

	var out bytes.Buffer
	if err := Run(&in, &out, Options{OutputDir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msg, err := ReadMessage(&out)
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["type"] != "sessions" {
		t.Fatalf("response type = %v, want sessions", resp["type"])
	}
	sessions := resp["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	first := sessions[0].(map[string]any)
	if first["id"] != "s1" {
		t.Errorf("sessions[0].id = %v, want s1", first["id"])
	}
}

func TestRunWritesVoiceFileWhenAudioChunksArrive(t *testing.T) {
	dir := t.TempDir()
	chunk1 := []byte{0x1a, 0x45, 0xdf, 0xa3} // webm EBML header, arbitrary for the test
	chunk2 := []byte{0x01, 0x02, 0x03}

	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "session-start", "protocolVersion": float64(ProtocolVersion),
		"sessionId": "s1", "startedAtEpochMs": float64(1000),
		"viewport": map[string]any{"width": float64(100), "height": float64(100)},
		"audio":    map[string]any{"mimeType": "audio/webm;codecs=opus"},
	}))
	in.Write(frame(t, map[string]any{
		"type": "audio-chunk", "tMs": float64(0), "data": base64.StdEncoding.EncodeToString(chunk1),
	}))
	in.Write(frame(t, map[string]any{
		"type": "audio-chunk", "tMs": float64(250), "data": base64.StdEncoding.EncodeToString(chunk2),
	}))
	in.Write(frame(t, map[string]any{"type": "session-stop", "endedAtEpochMs": float64(1500)}))

	var out bytes.Buffer
	var completedDir string
	opts := Options{
		OutputDir:  dir,
		OnComplete: func(d string) { completedDir = d },
		OnFailed:   func(d, reason string) { t.Fatalf("unexpected failure: %s (%s)", reason, d) },
	}
	if err := Run(&in, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	voiceOnDisk, err := os.ReadFile(filepath.Join(completedDir, "voice.webm"))
	if err != nil {
		t.Fatalf("voice.webm was not written: %v", err)
	}
	want := append(append([]byte{}, chunk1...), chunk2...)
	if !bytes.Equal(voiceOnDisk, want) {
		t.Errorf("voice.webm bytes = %x, want %x (chunks appended in arrival order)", voiceOnDisk, want)
	}

	sessionBuf, err := os.ReadFile(filepath.Join(completedDir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(sessionBuf, &doc); err != nil {
		t.Fatal(err)
	}
	audio, ok := doc["audio"].(map[string]any)
	if !ok {
		t.Fatalf("session.json audio = %v, want an object", doc["audio"])
	}
	if audio["mimeType"] != "audio/webm;codecs=opus" {
		t.Errorf("session.json audio.mimeType = %v, want audio/webm;codecs=opus", audio["mimeType"])
	}
}

func TestRunLeavesNoVoiceFileWhenAudioAbsent(t *testing.T) {
	dir := t.TempDir()
	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "session-start", "protocolVersion": float64(ProtocolVersion),
		"sessionId": "s1", "startedAtEpochMs": float64(1000),
		"viewport": map[string]any{"width": float64(100), "height": float64(100)},
	}))
	in.Write(frame(t, map[string]any{"type": "session-stop", "endedAtEpochMs": float64(1500)}))

	var out bytes.Buffer
	var completedDir string
	opts := Options{
		OutputDir:  dir,
		OnComplete: func(d string) { completedDir = d },
		OnFailed:   func(d, reason string) { t.Fatalf("unexpected failure: %s (%s)", reason, d) },
	}
	if err := Run(&in, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(completedDir, "voice.webm")); !os.IsNotExist(err) {
		t.Errorf("voice.webm exists when the session carried no audio (err=%v) — the opt-in feature must be inert when off", err)
	}

	sessionBuf, err := os.ReadFile(filepath.Join(completedDir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(sessionBuf, &doc); err != nil {
		t.Fatal(err)
	}
	if _, present := doc["audio"]; present {
		t.Errorf(`session.json has an "audio" key when no audio was sent: %v`, doc["audio"])
	}
}

func TestRunRejectsAudioChunkBeforeSessionStart(t *testing.T) {
	dir := t.TempDir()
	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "audio-chunk", "tMs": float64(0), "data": base64.StdEncoding.EncodeToString([]byte{0x01}),
	}))

	var out bytes.Buffer
	var failedReason string
	opts := Options{
		OutputDir: dir,
		OnFailed:  func(d, reason string) { failedReason = reason },
	}
	if err := Run(&in, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if failedReason != "" {
		t.Errorf("OnFailed called with %q, want it not called (no writer was ever opened)", failedReason)
	}
	msg, err := ReadMessage(&out)
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["type"] != "error" {
		t.Errorf("response type = %v, want error", resp["type"])
	}
}

func TestValidateSessionStartRejectsWrongProtocolVersion(t *testing.T) {
	_, err := validateSessionStart(map[string]any{
		"protocolVersion": float64(1), "sessionId": "s", "startedAtEpochMs": float64(1),
		"viewport": map[string]any{"width": float64(1), "height": float64(1)},
	})
	if err == nil {
		t.Fatal("expected an error for a mismatched protocol version")
	}
}

func makeTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 10, G: 200, B: 90, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestRunProducesRawMp4FromRealFrames exercises the whole wiring docs/plans/
// 2026-08-22-segmented-plate-recording.md added: session.Writer opens an
// internal/plate.Recorder and stops it when the session ends, but — since a Chrome-killed
// process taught this the hard way — Run itself does not wait to assemble raw.mp4 (that could
// take longer than Chrome's few-second grace period between disconnecting a native-messaging
// host and killing it outright). So after Run returns: session.json exists with the real
// events/duration, frames/segments are still on disk untouched, and raw.mp4 does not exist
// yet — assembling it is unconditionally plate.EnsureRaw's job, run from a separate detached
// process. This test drives both halves to prove they fit together.
func TestRunProducesRawMp4FromRealFrames(t *testing.T) {
	if !render.IsFfmpegAvailable() {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	jpeg1, jpeg2 := makeTestJPEG(t), makeTestJPEG(t)

	var in bytes.Buffer
	in.Write(frame(t, map[string]any{
		"type": "session-start", "protocolVersion": float64(ProtocolVersion),
		"sessionId": "s1", "startedAtEpochMs": float64(1000),
		"url":      "https://example.com",
		"viewport": map[string]any{"width": float64(64), "height": float64(48), "devicePixelRatio": float64(1)},
	}))
	in.Write(frame(t, map[string]any{
		"type": "frame", "tMs": float64(0), "data": base64.StdEncoding.EncodeToString(jpeg1),
	}))
	in.Write(frame(t, map[string]any{
		"type": "frame", "tMs": float64(400), "data": base64.StdEncoding.EncodeToString(jpeg2),
	}))
	in.Write(frame(t, map[string]any{"type": "session-stop", "endedAtEpochMs": float64(1800)}))

	var out bytes.Buffer
	var completedDir string
	opts := Options{
		OutputDir:  dir,
		OnComplete: func(d string) { completedDir = d },
		OnFailed:   func(d, reason string) { t.Fatalf("unexpected failure: %s (%s)", reason, d) },
	}
	if err := Run(&in, &out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Run itself must not have built raw.mp4 — that would mean it waited on the background
	// encoder/full assembly again, the exact thing that got a real recording killed.
	if _, err := os.Stat(filepath.Join(completedDir, "raw.mp4")); !os.IsNotExist(err) {
		t.Fatalf("raw.mp4 exists right after Run returns (err=%v); assembling it must be deferred to EnsureRaw", err)
	}
	if _, err := os.Stat(filepath.Join(completedDir, "frames")); os.IsNotExist(err) {
		t.Fatal("frames/ is already gone right after Run returns; it should survive until EnsureRaw publishes raw.mp4")
	}

	rawPath, err := plate.EnsureRaw(completedDir, t.Logf)
	if err != nil {
		t.Fatalf("EnsureRaw: %v", err)
	}
	if _, err := render.ProbeVideo(rawPath); err != nil {
		t.Fatalf("raw.mp4 was not produced or does not probe as a video: %v", err)
	}
	if _, err := os.Stat(filepath.Join(completedDir, "frames")); !os.IsNotExist(err) {
		t.Errorf("frames/ still exists after a successful EnsureRaw (err=%v)", err)
	}
}
