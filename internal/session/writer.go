// Package session writes the immutable raw session: frames/segments (via internal/plate) +
// session.json + debug.log. Nothing here makes post-production decisions — session.json's
// stage order (voice -> analyze -> render -> compact) lives in internal/postproduction.
//
// Direct port of src/receiver/session-writer.js, since extended with internal/plate for
// docs/plans/2026-08-22-segmented-plate-recording.md.
package session

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"take5/internal/plate"
)

// Version is the schema version of session.json.
const Version = 2

// VoiceFile is the raw mic capture, written exactly as MediaRecorder produced it — a webm/
// opus container, not a WAV. The plan's own prose (docs/plans/2026-08-19-voice-annotations.md)
// calls this "voice.wav" as a stand-in for "the raw voice recording"; browsers' MediaRecorder
// has no WAV/PCM output mode, only encoded containers, so a real WAV can only exist after an
// FFmpeg transcode — which belongs to the `voice` stage (Task 3), not here. This package's own
// stated invariant is that it makes no post-production decisions and needs no FFmpeg (see the
// package doc comment); writing the container Chrome actually gave it, under its real name, is
// the raw-capture equivalent of frames/*.jpg staying JPEGs instead of being decoded here.
const VoiceFile = "voice.webm"

// JournalFile records one JSON line per event/network record the instant it arrives — append-
// only, never rewritten, unlike session.json which needs a single coherent sorted document.
// debug.log and internal/plate's frames/segments already follow this same "durable in small
// pieces, not one all-or-nothing rewrite" shape; events/network were the one piece of session
// state that didn't, until Recover started reading this back in.
const JournalFile = "journal.jsonl"

// journalEntry is one JournalFile line: exactly one of Event or Network is set, per Type.
type journalEntry struct {
	Type    string         `json:"type"`
	Event   map[string]any `json:"event,omitempty"`
	Network *NetworkRecord `json:"network,omitempty"`
}

// Viewport describes the capture dimensions and DPI.
type Viewport struct {
	Width            float64 `json:"width"`
	Height           float64 `json:"height"`
	DevicePixelRatio float64 `json:"devicePixelRatio"`
}

// StartInfo is the validated content of a session-start message — what the writer needs to
// open a session directory and seed session.json.
type StartInfo struct {
	SessionID        string
	StartedAtEpochMs float64
	URL              string
	Viewport         Viewport
	Capture          map[string]any
	Audio            *AudioInfo
}

// AudioInfo records the raw mic-capture container format when voice narration is opted in
// (docs/plans/2026-08-19-voice-annotations.md Task 2). Nil means the recording carries no
// voice.webm at all — the pre-feature, byte-identical path.
type AudioInfo struct {
	MimeType string `json:"mimeType"`
}

// NetworkRecord is a captured HTTP request.
type NetworkRecord struct {
	ID      string  `json:"id"`
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
	Type    string  `json:"type"`
	Status  float64 `json:"status"`
	Failed  bool    `json:"failed"`
}

// Document is what finalize/abort write to session.json.
type Document struct {
	Version          int              `json:"version"`
	SessionID        string           `json:"sessionId"`
	StartedAtEpochMs float64          `json:"startedAtEpochMs"`
	DurationMs       int64            `json:"durationMs"`
	URL              string           `json:"url"`
	Viewport         Viewport         `json:"viewport"`
	Capture          map[string]any   `json:"capture"`
	Audio            *AudioInfo       `json:"audio,omitempty"`
	Events           []map[string]any `json:"events"`
	Network          []NetworkRecord  `json:"network"`
}

// Writer captures a session: frames, events, network activity.
type Writer struct {
	Dir     string
	start   StartInfo
	events  []map[string]any
	network []NetworkRecord
	plate   *plate.Recorder

	frameCount  int
	lastFrameMs int64

	bytesReceived      int64
	logFile            *os.File
	journalFile        *os.File
	voiceFile          *os.File
	voiceBytesReceived int64
}

// DirName produces a human-sortable directory name, as in the spec's demo-output/2026-08-17-120102/.
func DirName(epochMs float64) string {
	t := time.UnixMilli(int64(epochMs))
	return fmt.Sprintf(
		"%04d-%02d-%02d-%02d%02d%02d",
		t.Year(), int(t.Month()), t.Day(),
		t.Hour(), t.Minute(), t.Second(),
	)
}

// Two recordings started in the same second must not land in the same directory and
// overwrite each other's raw video.
func uniqueDir(outputDir, name string) (string, error) {
	for suffix := 0; ; suffix++ {
		candidate := name
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d", name, suffix+1)
		}
		full := filepath.Join(outputDir, candidate)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			return full, nil
		} else if err != nil {
			return "", fmt.Errorf("check directory: %w", err)
		}
	}
}

// Open creates a new session writer in a unique directory.
func Open(outputDir string, start StartInfo) (*Writer, error) {
	dir, err := uniqueDir(outputDir, DirName(start.StartedAtEpochMs))
	if err != nil {
		return nil, err
	}
	if mkdirErr := os.MkdirAll(dir, 0o750); mkdirErr != nil { // #nosec G301
		return nil, fmt.Errorf("create session directory: %w", mkdirErr)
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G302 G304
	if err != nil {
		return nil, fmt.Errorf("open debug.log: %w", err)
	}
	journalFile, err := os.OpenFile(filepath.Join(dir, JournalFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G302 G304
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("open journal: %w", err)
	}

	w := &Writer{Dir: dir, start: start, logFile: logFile, journalFile: journalFile}
	plateRecorder, err := plate.Open(dir, func(format string, args ...any) { w.Log(fmt.Sprintf(format, args...)) })
	if err != nil {
		_ = journalFile.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("open plate recorder: %w", err)
	}
	w.plate = plateRecorder
	viewportJSON, _ := json.Marshal(start.Viewport)
	captureJSON, _ := json.Marshal(start.Capture)
	w.Log(fmt.Sprintf("session %s started at %v", start.SessionID, start.StartedAtEpochMs))
	w.Log(fmt.Sprintf("viewport %s", viewportJSON))
	w.Log(fmt.Sprintf("capture %s", captureJSON))
	// sessionId/url/viewport/capture are known now and never change again; writing them
	// immediately means even a session that dies before its first event still leaves a
	// parseable session.json (durationMs 0, empty events/network) instead of none at all.
	w.checkpoint()
	return w, nil
}

// Log writes a debug log entry.
func (w *Writer) Log(line string) {
	if w.logFile == nil {
		return
	}
	fmt.Fprintf(w.logFile, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), line)
}

// AppendFrame saves a screen capture. Physical storage — JPEG naming, incremental segment
// encoding, cleanup — belongs to internal/plate; this keeps only the counters debug.log and
// Abort's implied-duration fallback need.
func (w *Writer) AppendFrame(tMs int64, data []byte) error {
	w.bytesReceived += int64(len(data))
	if err := w.plate.AppendFrame(tMs, data); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	w.frameCount++
	w.lastFrameMs = tMs
	return nil
}

// AppendAudio writes one relayed MediaRecorder chunk to voice.webm, opened lazily on first
// use so a session with the voice toggle off never creates the file at all — the file's mere
// existence is what "opt-in, off by default" means on disk. Chunks arrive already in
// recording order (the extension relays them through one sequential chain, mirroring how
// video frames are relayed — see service-worker.js), so this only ever appends.
func (w *Writer) AppendAudio(data []byte) error {
	if w.voiceFile == nil {
		f, err := os.OpenFile(filepath.Join(w.Dir, VoiceFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("could not open %s: %w", VoiceFile, err)
		}
		w.voiceFile = f
	}
	w.voiceBytesReceived += int64(len(data))
	if _, err := w.voiceFile.Write(data); err != nil {
		return fmt.Errorf("could not write to %s: %w", VoiceFile, err)
	}
	return nil
}

// AddEvent records a user interaction event, in memory (for Finalize/Abort's own write) and in
// JournalFile (durably, so it survives a hard kill that never reaches Finalize/Abort at all).
func (w *Writer) AddEvent(event map[string]any) {
	w.events = append(w.events, event)
	w.appendJournal(journalEntry{Type: "event", Event: event})
}

// AddNetworkRecord records an HTTP request, for the same two reasons AddEvent does.
func (w *Writer) AddNetworkRecord(record NetworkRecord) {
	w.network = append(w.network, record)
	w.appendJournal(journalEntry{Type: "network", Network: &record})
}

// appendJournal writes one entry to JournalFile — a single write() syscall on an append-mode
// file descriptor, the same durability debug.log's lines and internal/plate's frame/segment
// writes already rely on. Best-effort: a failure is logged, not fatal, matching this package's
// established rule that a durability hiccup must not interrupt capture.
func (w *Writer) appendJournal(entry journalEntry) {
	if w.journalFile == nil {
		return
	}
	buf, err := json.Marshal(entry)
	if err != nil {
		w.Log(fmt.Sprintf("journal: could not marshal entry: %s", err))
		return
	}
	if _, err := w.journalFile.Write(append(buf, '\n')); err != nil {
		w.Log(fmt.Sprintf("journal: %s", err))
	}
}

func eventEndMs(e map[string]any) float64 {
	if v, ok := e["endMs"]; ok && v != nil {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	if f, ok := e["t"].(float64); ok {
		return f
	}
	return 0
}

func eventT(e map[string]any) float64 {
	if f, ok := e["t"].(float64); ok {
		return f
	}
	return 0
}

// sortedEvents and sortedNetwork are shared by writeSessionJSON and Recover — both ultimately
// produce the same canonical, time-ordered Document, just from different sources (the
// in-memory slice vs. JournalFile replayed after a hard kill). sort.SliceStable, not
// sort.Slice: ties on the same millisecond must keep arrival order, exactly as
// Array.prototype.sort does in the Node implementation this ports (docs/plans/go-port.md,
// "traps to write tests for first").
func sortedEvents(events []map[string]any) []map[string]any {
	out := append([]map[string]any{}, events...)
	sort.SliceStable(out, func(i, j int) bool { return eventT(out[i]) < eventT(out[j]) })
	return out
}

func sortedNetwork(network []NetworkRecord) []NetworkRecord {
	out := append([]NetworkRecord{}, network...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

func lastEventEndMs(events []map[string]any) float64 {
	var last float64
	for _, e := range events {
		if end := eventEndMs(e); end > last {
			last = end
		}
	}
	return last
}

func (w *Writer) impliedDurationMs() int64 {
	lastEvent := int64(lastEventEndMs(w.events))
	if lastEvent > w.lastFrameMs {
		return lastEvent
	}
	return w.lastFrameMs
}

func (w *Writer) writeSessionJSON(durationMs int64) (Document, error) {
	doc := Document{
		Version:          Version,
		SessionID:        w.start.SessionID,
		StartedAtEpochMs: w.start.StartedAtEpochMs,
		DurationMs:       durationMs,
		URL:              w.start.URL,
		Viewport:         w.start.Viewport,
		Capture:          w.start.Capture,
		Audio:            w.start.Audio,
		Events:           sortedEvents(w.events),
		Network:          sortedNetwork(w.network),
	}
	if doc.Capture == nil {
		doc.Capture = map[string]any{}
	}

	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return doc, fmt.Errorf("marshal session document: %w", err)
	}
	path := filepath.Join(w.Dir, "session.json")
	part := path + ".part"
	if writeErr := os.WriteFile(part, append(buf, '\n'), 0o600); writeErr != nil { // #nosec G306
		return doc, fmt.Errorf("write session.json: %w", writeErr)
	}
	if renameErr := os.Rename(part, path); renameErr != nil {
		return doc, fmt.Errorf("commit session.json: %w", renameErr)
	}
	return doc, nil
}

// checkpoint writes session.json once, at Open — sessionId/url/viewport/capture are known
// immediately and never change again, so even a session that dies before its first event still
// leaves a parseable session.json (durationMs 0, empty events/network) rather than none at
// all. It is deliberately not called again on every AddEvent/AddNetworkRecord: that would
// rewrite the whole (growing) document on every single one, when JournalFile already makes
// each event/network record durable the instant it arrives, in O(1), without rewriting
// anything. A production recording was lost with session.json entirely absent — evidence the
// process never got even this first write — which is what motivated writing it this early.
func (w *Writer) checkpoint() {
	if _, err := w.writeSessionJSON(w.impliedDurationMs()); err != nil {
		w.Log(fmt.Sprintf("checkpoint: %s", err))
	}
}

func (w *Writer) closeVoice() {
	if w.voiceFile != nil {
		_ = w.voiceFile.Close()
		w.voiceFile = nil
	}
}

func (w *Writer) closeLog() {
	w.closeVoice()
	if w.journalFile != nil {
		_ = w.journalFile.Close()
		w.journalFile = nil
	}
	if w.logFile != nil {
		_ = w.logFile.Close()
		w.logFile = nil
	}
}

// Finalize writes the completed session and closes it out. Mirrors SessionWriter.finalize.
// It does not wait for raw.mp4 to exist — plate.Recorder.Stop returns immediately, and
// building raw.mp4 is unconditionally internal/postproduction's job via plate.EnsureRaw, run
// from a detached process afterward. Finalize used to wait for that inline, which is exactly
// what got a live recording killed by Chrome: a disconnected native-messaging host gets only
// a few seconds to exit before Chrome kills it outright, and assembling raw.mp4 (draining a
// background FFmpeg queue, then concatenating every segment) can easily take longer than
// that. See internal/plate.Recorder.Stop's doc comment for the full account.
func (w *Writer) Finalize(endedAtEpochMs float64) (Document, error) {
	durationMs := int64(math.Max(0, math.Round(endedAtEpochMs-w.start.StartedAtEpochMs)))
	// voice.webm must be flushed and closed before EnsureRaw reads it later, or FFmpeg would
	// see a truncated file.
	w.closeVoice()
	w.plate.Stop()
	doc, err := w.writeSessionJSON(durationMs)
	if err != nil {
		return Document{}, fmt.Errorf("write session: %w", err)
	}
	w.Log(fmt.Sprintf(
		"finalized: %d frames, %d bytes, %d events, %d network records, %d voice bytes",
		w.frameCount, w.bytesReceived, len(w.events), len(w.network), w.voiceBytesReceived,
	))
	w.closeLog()
	return doc, nil
}

// Abort keeps everything received so far on purpose (spec 26): the frames and segments
// written so far are still a usable plate, so the recording can be salvaged by hand or by a
// later plate.EnsureRaw.
func (w *Writer) Abort(reason string) error {
	w.Log(fmt.Sprintf("aborted: %s", reason))
	durationMs := w.impliedDurationMs()
	w.plate.Stop()
	if _, err := w.writeSessionJSON(durationMs); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	w.closeLog()
	return nil
}
