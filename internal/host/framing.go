// Package host implements the local process Chrome talks to over native messaging: stdio
// framing and the session lifecycle. Direct replacement for the WebSocket receiver in
// src/receiver/server.js — see docs/plans/go-port.md, "Spike 1" in SPIKE.md.
package host

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Chrome's own limit on a single native message is 1 MiB host→extension and 64 MiB
// extension→host (SPIKE.md §1 consequences). A JPEG screencast frame is a few hundred KB;
// this is well clear of any legitimate message and only guards against a corrupt stream.
const maxIncomingMessageBytes = 100 * 1024 * 1024

// ReadMessage reads one native-messaging frame: a 4-byte length prefix in native byte
// order, then that many bytes of UTF-8 JSON. Every platform this ships for is little-endian.
func ReadMessage(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read message length: %w", err)
	}
	n := binary.LittleEndian.Uint32(lenBuf[:])
	if n > maxIncomingMessageBytes {
		return nil, fmt.Errorf("message of %d bytes exceeds the %d byte limit", n, maxIncomingMessageBytes)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read message payload: %w", err)
	}
	return buf, nil
}

// WriteMessage writes one native-messaging frame. stdout *is* the protocol (SPIKE.md §1
// consequences) — nothing else may write to it, which is why cmdHost sends every log line
// to stderr instead.
func WriteMessage(w io.Writer, payload []byte) error {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload))) // #nosec G115
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write message length: %w", err)
	}
	_, err := w.Write(payload)
	if err != nil {
		return fmt.Errorf("write message payload: %w", err)
	}
	return nil
}

// WriteJSON marshals v to JSON and writes it as a framed message.
func WriteJSON(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	return WriteMessage(w, payload)
}
