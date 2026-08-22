package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Recover heals dir's session.json in place when it exists but has no events/network of its
// own — meaning Finalize/Abort's sort-and-merge never ran at all, because the process was
// killed before it could (session.json still gets written once, immediately, at Open — see
// checkpoint — so this is the shape a hard kill leaves, not a missing file). It replays
// JournalFile, the durable per-event/network log AddEvent/AddNetworkRecord already write, and
// rewrites session.json (atomically) with what that replay found. A session.json that already
// has real events/network (the normal completed-recording shape) is left untouched — the
// journal is only ever a fallback source, never authoritative over what Finalize/Abort wrote.
// Returns whether it changed anything.
func Recover(dir string) (bool, error) {
	sessionPath := filepath.Join(dir, "session.json")
	buf, err := os.ReadFile(sessionPath) //nolint:gosec // G304: dir is a caller-provided session directory
	if err != nil {
		return false, fmt.Errorf("read session.json: %w", err)
	}
	var doc Document
	if unmarshalErr := json.Unmarshal(buf, &doc); unmarshalErr != nil {
		return false, fmt.Errorf("could not parse session.json: %w", unmarshalErr)
	}
	if len(doc.Events) > 0 || len(doc.Network) > 0 {
		return false, nil
	}

	events, network, err := readJournal(filepath.Join(dir, JournalFile))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read journal: %w", err)
	}
	if len(events) == 0 && len(network) == 0 {
		return false, nil
	}

	doc.Events = sortedEvents(events)
	doc.Network = sortedNetwork(network)
	if implied := int64(lastEventEndMs(events)); implied > doc.DurationMs {
		doc.DurationMs = implied
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal recovered session document: %w", err)
	}
	part := sessionPath + ".part"
	if writeErr := os.WriteFile(part, append(out, '\n'), 0o600); writeErr != nil { // #nosec G306
		return false, fmt.Errorf("write recovered session.json: %w", writeErr)
	}
	if renameErr := os.Rename(part, sessionPath); renameErr != nil {
		return false, fmt.Errorf("commit recovered session.json: %w", renameErr)
	}
	return true, nil
}

// readJournal replays JournalFile into its two event/network slices. A line that fails to
// parse is skipped, not fatal: the one line a kill interrupted mid-write is expected to be
// truncated garbage, and everything recorded before it is still good — one bad line must not
// block recovering the rest.
func readJournal(path string) ([]map[string]any, []NetworkRecord, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is derived from a caller-provided session directory
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // unwrapped so Recover's os.IsNotExist(err) check still sees it — fmt.Errorf's %w defeats that
	}
	defer f.Close()

	var events []map[string]any
	var network []NetworkRecord
	scanner := bufio.NewScanner(f)
	// A single event/network record is a few hundred bytes at most; 1 MiB is generous
	// headroom, not a real limit anyone should hit.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry journalEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		switch entry.Type {
		case "event":
			events = append(events, entry.Event)
		case "network":
			if entry.Network != nil {
				network = append(network, *entry.Network)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return events, network, fmt.Errorf("scan journal: %w", err)
	}
	return events, network, nil
}
