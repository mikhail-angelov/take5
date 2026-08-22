package host

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"

	"take5/internal/session"
)

// ProtocolVersion 3: unlike the WebSocket wire contract it replaces (protocol.js's
// PROTOCOL_VERSION 2), native messaging has no side channel for binary frames, so a frame
// header and its bytes collapse into one JSON message with a base64 payload — see
// validateFrame. The field-level validation below otherwise ports as-is from
// src/receiver/protocol.js (docs/plans/go-port.md, Inventory).
const ProtocolVersion = 3

var eventKinds = map[string]struct{}{
	"pointer":  {},
	"click":    {},
	"input":    {},
	"shortcut": {},
	"scroll":   {},
	"drag":     {},
}

func asFloat(v any) (float64, bool) {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

func isPositiveNumber(v any) bool {
	n, ok := asFloat(v)
	return ok && n > 0
}

func asString(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func asObject(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func validateSessionStart(msg map[string]any) (session.StartInfo, error) {
	pv, ok := asFloat(msg["protocolVersion"])
	if !ok || int(pv) != ProtocolVersion {
		return session.StartInfo{}, fmt.Errorf(
			"unsupported protocolVersion %v, expected %d", msg["protocolVersion"], ProtocolVersion,
		)
	}
	sessionID, _ := msg["sessionId"].(string)
	if sessionID == "" {
		return session.StartInfo{}, errors.New("sessionId is required")
	}
	startedAt, ok := asFloat(msg["startedAtEpochMs"])
	if !ok || startedAt <= 0 {
		return session.StartInfo{}, errors.New("startedAtEpochMs is required")
	}

	viewportRaw := asObject(msg["viewport"])
	width, wOk := asFloat(viewportRaw["width"])
	height, hOk := asFloat(viewportRaw["height"])
	if !wOk || width <= 0 || !hOk || height <= 0 {
		return session.StartInfo{}, errors.New("viewport width and height are required")
	}
	dpr := 1.0
	if isPositiveNumber(viewportRaw["devicePixelRatio"]) {
		dpr, _ = asFloat(viewportRaw["devicePixelRatio"])
	}

	return session.StartInfo{
		SessionID:        sessionID,
		StartedAtEpochMs: startedAt,
		URL:              asString(msg["url"], ""),
		Viewport:         session.Viewport{Width: width, Height: height, DevicePixelRatio: dpr},
		Capture:          asObject(msg["capture"]),
		Audio:            validateAudio(msg["audio"]),
	}, nil
}

// validateAudio reads the optional voice-capture descriptor a session-start message carries
// when the extension's opt-in toggle is on (docs/plans/2026-08-19-voice-annotations.md
// Task 2/9). Absent or malformed means no voice.webm for this session — the pre-feature path.
func validateAudio(v any) *session.AudioInfo {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	mimeType, _ := obj["mimeType"].(string)
	if mimeType == "" {
		return nil
	}
	return &session.AudioInfo{MimeType: mimeType}
}

type frameInfo struct {
	TMs   int64
	Bytes []byte
}

// decodeTimedPayload is the shared shape behind "frame" and "audio-chunk": a tMs offset plus
// a base64 payload standing in for the WebSocket receiver's binary message (see
// ProtocolVersion). label names the field in error messages so a bad frame and a bad audio
// chunk are still told apart in the log.
func decodeTimedPayload(msg map[string]any, label string) (frameInfo, error) {
	tMs, ok := asFloat(msg["tMs"])
	if !ok || tMs < 0 {
		return frameInfo{}, fmt.Errorf("%s.tMs is required", label)
	}
	dataB64, ok := msg["data"].(string)
	if !ok || dataB64 == "" {
		return frameInfo{}, fmt.Errorf("%s.data is required", label)
	}
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return frameInfo{}, fmt.Errorf("%s.data is not valid base64: %w", label, err)
	}
	if len(raw) == 0 {
		return frameInfo{}, fmt.Errorf("%s.bytes is required", label)
	}
	return frameInfo{TMs: int64(math.Round(tMs)), Bytes: raw}, nil
}

func validateFrame(msg map[string]any) (frameInfo, error) {
	return decodeTimedPayload(msg, "frame")
}

// validateAudioChunk decodes one relayed MediaRecorder chunk (docs/plans/
// 2026-08-19-voice-annotations.md Task 2). tMs is carried for parity with "frame" and for
// debug logging only — session.Writer.AppendAudio appends chunks in arrival order and does
// not seek on it, since the extension relays them through one sequential chain.
func validateAudioChunk(msg map[string]any) (frameInfo, error) {
	return decodeTimedPayload(msg, "audio-chunk")
}

func validateEvent(v any) (map[string]any, error) {
	event, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("event is not an object")
	}
	kind, _ := event["kind"].(string)
	if _, known := eventKinds[kind]; !known {
		return nil, fmt.Errorf("unknown event kind %v", event["kind"])
	}
	if _, ok := asFloat(event["t"]); !ok {
		return nil, errors.New("event.t is required")
	}
	return event, nil
}

func validateNetworkRecord(v any) (session.NetworkRecord, error) {
	record, ok := v.(map[string]any)
	if !ok {
		return session.NetworkRecord{}, errors.New("record is not an object")
	}
	startMs, ok := asFloat(record["startMs"])
	if !ok {
		return session.NetworkRecord{}, errors.New("record.startMs is required")
	}
	endMs, ok := asFloat(record["endMs"])
	if !ok {
		return session.NetworkRecord{}, errors.New("record.endMs is required")
	}
	id := ""
	if v, present := record["id"]; present && v != nil {
		id = fmt.Sprint(v)
	}
	status, _ := asFloat(record["status"])
	failed, _ := record["failed"].(bool)

	return session.NetworkRecord{
		ID:      id,
		StartMs: startMs,
		EndMs:   endMs,
		Type:    asString(record["type"], "other"),
		Status:  status,
		Failed:  failed,
	}, nil
}
