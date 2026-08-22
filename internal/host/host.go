package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"take5/internal/session"
)

// Options configures one Run over one native-messaging port. Native messaging is inherently
// one caller per process — Chrome spawns a fresh host per connectNative call — so unlike
// server.js there is no "a recording is already in progress" branch to port: the OS-level
// equivalent is simply not spawning a second host, which install/doctor's manifest already
// guarantees per extension id.
type Options struct {
	OutputDir string

	// CallerOrigin is argv[1], which Chrome sets to the connecting extension's origin
	// (chrome-extension://<id>/). AllowedOrigin is what install registered. When both are
	// set they must match — independent verification of the caller, per SPIKE.md §1.3.
	CallerOrigin  string
	AllowedOrigin string

	Logf func(format string, args ...any)
	// OnSessionStart fires the moment a session directory exists (before any frame/event has
	// been written), so a caller that has nowhere durable to log to before this point — see
	// cmd/take5's bootstrap/heartbeat/panic logging — has one from here on.
	OnSessionStart func(dir string)
	OnComplete     func(dir string)
	OnFailed       func(dir, reason string)
}

// Run reads native-messaging frames from r until Chrome closes the port (r hits EOF) or r is
// closed out from under it (os.ErrClosed — cmd/take5's host command does this from a
// signal handler so a SIGTERM still runs Abort/Finalize instead of dropping the recording with
// no session.json), and writes responses to w. Port of the message dispatch in
// src/receiver/server.js, adapted for a single stdio connection instead of a WebSocketServer.
func Run(r io.Reader, w io.Writer, opts Options) error {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if opts.OnSessionStart == nil {
		opts.OnSessionStart = func(string) {}
	}
	if opts.OnComplete == nil {
		opts.OnComplete = func(string) {}
	}
	if opts.OnFailed == nil {
		opts.OnFailed = func(string, string) {}
	}
	if opts.AllowedOrigin != "" && opts.CallerOrigin != "" && opts.CallerOrigin != opts.AllowedOrigin {
		return fmt.Errorf("caller origin %q does not match the registered extension %q", opts.CallerOrigin, opts.AllowedOrigin)
	}

	var writer *session.Writer
	var pendingStop *float64

	fail := func(reason string) {
		opts.Logf("session failed: %s", reason)
		_ = WriteJSON(w, map[string]any{"type": "error", "error": reason})
		if writer != nil {
			dir := writer.Dir
			if err := writer.Abort(reason); err != nil {
				opts.Logf("abort failed: %s", err)
			}
			opts.OnFailed(dir, reason)
			writer = nil
		}
	}

	for {
		payload, err := ReadMessage(r)
		if err != nil {
			// os.ErrClosed: a production recording was lost with no session.json and no
			// "aborted:" line in debug.log at all — evidence the process was terminated
			// (SIGTERM, most likely, since Chrome/the OS can send it during teardown) with no
			// chance to run any of the code below. cmd/take5's host command now
			// catches that signal and closes stdin itself so this read unblocks here — the
			// same graceful shutdown path as Chrome closing the port normally — instead of
			// letting the default signal disposition kill the process with zero cleanup.
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed) {
				break
			}
			return err
		}

		var msg map[string]any
		if err := json.Unmarshal(payload, &msg); err != nil {
			fail("message is not valid JSON")
			continue
		}
		msgType, _ := msg["type"].(string)
		if msgType == "" {
			fail("message has no type")
			continue
		}

		switch msgType {
		case "session-start":
			writer = handleSessionStart(msg, opts.OutputDir, writer, w, opts.Logf, opts.OnSessionStart, fail)
		case "frame":
			handleFrame(msg, writer, fail)
		case "audio-chunk":
			handleAudioChunk(msg, writer, fail)
		case "event":
			handleEvent(msg, writer)
		case "network":
			handleNetwork(msg, writer)
		case "list-sessions":
			handleListSessions(opts.OutputDir, w, fail)
		case "session-stop":
			pendingStop = handleSessionStop(msg, writer, fail)
		default:
			if writer != nil {
				writer.Log(fmt.Sprintf("ignored message type %s", msgType))
			}
		}
	}

	return finializeSession(w, writer, pendingStop, opts)
}

func handleSessionStart(msg map[string]any, outputDir string, existingWriter *session.Writer, w io.Writer, logf func(string, ...any), onSessionStart, fail func(string)) *session.Writer {
	if existingWriter != nil {
		fail("session-start received twice on one connection")
		return existingWriter
	}
	start, err := validateSessionStart(msg)
	if err != nil {
		fail(err.Error())
		return nil
	}
	opened, err := session.Open(outputDir, start)
	if err != nil {
		fail(fmt.Sprintf("could not open session directory: %s", err))
		return nil
	}
	onSessionStart(opened.Dir)
	logf("recording -> %s", opened.Dir)
	_ = WriteJSON(w, map[string]any{"type": "session-accepted", "dir": opened.Dir})
	return opened
}

func handleFrame(msg map[string]any, writer *session.Writer, fail func(string)) {
	if writer == nil {
		fail("received a frame before session-start")
		return
	}
	frame, err := validateFrame(msg)
	if err != nil {
		fail(err.Error())
		return
	}
	if err := writer.AppendFrame(frame.TMs, frame.Bytes); err != nil {
		fail(fmt.Sprintf("could not write frame: %s", err))
	}
}

func handleAudioChunk(msg map[string]any, writer *session.Writer, fail func(string)) {
	if writer == nil {
		fail("received an audio chunk before session-start")
		return
	}
	chunk, err := validateAudioChunk(msg)
	if err != nil {
		fail(err.Error())
		return
	}
	if err := writer.AppendAudio(chunk.Bytes); err != nil {
		fail(fmt.Sprintf("could not write audio chunk: %s", err))
	}
}

func handleEvent(msg map[string]any, writer *session.Writer) {
	if writer == nil {
		return
	}
	event, err := validateEvent(msg["event"])
	if err != nil {
		writer.Log(fmt.Sprintf("dropped event: %s", err))
		return
	}
	writer.AddEvent(event)
}

func handleNetwork(msg map[string]any, writer *session.Writer) {
	if writer == nil {
		return
	}
	record, err := validateNetworkRecord(msg["record"])
	if err != nil {
		writer.Log(fmt.Sprintf("dropped network record: %s", err))
		return
	}
	writer.AddNetworkRecord(record)
}

func handleListSessions(outputDir string, w io.Writer, fail func(string)) {
	summaries, err := ListSessions(outputDir)
	if err != nil {
		fail(fmt.Sprintf("could not list sessions: %s", err))
		return
	}
	_ = WriteJSON(w, map[string]any{"type": "sessions", "sessions": summaries})
}

func handleSessionStop(msg map[string]any, writer *session.Writer, fail func(string)) *float64 {
	if writer == nil {
		fail("session-stop without session-start")
		return nil
	}
	// Durably record the one message whose arrival time matters most: everything that
	// follows (Finalize, the session-finalized ack, OnComplete) is inferred from its absence
	// when something goes wrong, and this is the only direct evidence of when — or whether —
	// it was received at all. A production incident went undiagnosed for several rounds partly
	// because nothing logged the receipt of session-stop itself, only its downstream effects.
	writer.Log("received session-stop")
	endedAt, ok := asFloat(msg["endedAtEpochMs"])
	if !ok {
		endedAt = float64(time.Now().UnixMilli())
	}
	return &endedAt
}

func finializeSession(w io.Writer, writer *session.Writer, pendingStop *float64, opts Options) error {
	if writer == nil {
		return nil
	}

	if pendingStop == nil {
		dir := writer.Dir
		if err := writer.Abort("connection closed before session-stop"); err != nil {
			return fmt.Errorf("abort session: %w", err)
		}
		opts.OnFailed(dir, "connection closed before session-stop")
		return nil
	}

	doc, err := writer.Finalize(*pendingStop)
	if err != nil {
		return fmt.Errorf("finalize session: %w", err)
	}
	opts.Logf("recorded %d ms -> %s", doc.DurationMs, writer.Dir)
	// OnComplete (which spawns the detached render process — the thing that actually matters)
	// runs before the session-finalized ack is even attempted, deliberately: the ack write is
	// a courtesy to the extension, not something anything downstream depends on, and it must
	// never be able to delay or risk the one call that has to happen. A production incident
	// (see docs/plans/2026-08-22-segmented-plate-recording.md) had OnComplete's own first log
	// line simply never appear, and this write — introduced at the same time — was the only
	// new thing standing between it and the "recorded ... ms" line right above; ordering it
	// after removes any doubt, whatever the actual mechanism was.
	opts.OnComplete(writer.Dir)
	// Best-effort and after the fact: if the port is already gone (the extension's own
	// stopRecording disconnects without waiting, on any build older than this one), there is
	// no one left to tell, and that must never be able to affect anything above this line.
	_ = WriteJSON(w, map[string]any{"type": "session-finalized", "durationMs": doc.DurationMs})
	return nil
}
