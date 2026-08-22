// Package postproduction owns a session directory's artifact lifecycle: canonical artifact
// names, freshness/invalidation rules between session.json, voice.json, project.json and
// demo.mp4, the plate → optional voice → analyze → render → compact stage order, and status
// inspection for history — so the CLI, the automatic post-recording path, and history all call
// one thing instead of re-deriving these rules.
package postproduction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"take5/internal/director"
	"take5/internal/plate"
	"take5/internal/render"
	"take5/internal/session"
)

// Canonical artifact filenames within a session directory that this package itself owns.
// session.VoiceFile ("voice.webm", internal/session's own raw mic capture) and plate.RawFile
// are reused rather than redeclared here — SessionFile always exists once a recording
// completes; VoiceCuesFile is the voice stage's own output; ProjectFile and OutputFile are
// render's derived plan and final video; DebugLogFile is the durable progress/failure record
// Status reads back, since the render process runs detached from the host with no other
// channel to report through.
const (
	SessionFile   = "session.json"
	VoiceCuesFile = "voice.json"
	ProjectFile   = "project.json"
	OutputFile    = "demo.mp4"
	DebugLogFile  = "debug.log"
)

// VoiceStage attempts the optional narration pipeline (transcribe -> rewrite -> TTS) for dir
// and reports whether it wrote a fresh voice.json. A caller with nothing to try — no
// credentials configured, no whisper.cpp model — returns (false, nil) after logging why:
// narration is additive, never a reason Render should fail or even count as having failed to
// try.
type VoiceStage func(ctx context.Context, dir string) (ran bool, err error)

// Options configures Render's optional stages.
type Options struct {
	Logf func(format string, args ...any)
	// Voice is attempted when dir has voice.webm waiting but no voice.json yet. Nil means
	// narration is never attempted.
	Voice VoiceStage
}

func (o Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Analyze reads dir's session.json (plus voice.json, if narration has already been processed),
// runs the Director, and writes project.json.
func Analyze(dir string) (director.Project, error) {
	// dir is a caller-provided session directory (a CLI arg, or one internal/host itself just
	// created), not attacker-controlled input.
	sessionBuf, err := os.ReadFile(filepath.Join(dir, SessionFile)) //nolint:gosec // G304
	if err != nil {
		return director.Project{}, fmt.Errorf("could not read %s: %w", SessionFile, err)
	}
	var sess director.Session
	if unmarshalErr := json.Unmarshal(sessionBuf, &sess); unmarshalErr != nil {
		return director.Project{}, fmt.Errorf("could not parse %s: %w", SessionFile, unmarshalErr)
	}

	// A missing voice.json is the normal no-narration path: internal/voiceover only writes it
	// once every cue has succeeded, so a failed or partial voice run never leaves one behind.
	// A voice.json that exists but fails to parse is a different case — a real error.
	voiceBuf, voiceErr := os.ReadFile(filepath.Join(dir, VoiceCuesFile)) //nolint:gosec // G304
	switch {
	case voiceErr == nil:
		var voiceCues []director.VoiceCue
		if unmarshalErr := json.Unmarshal(voiceBuf, &voiceCues); unmarshalErr != nil {
			return director.Project{}, fmt.Errorf("could not parse %s: %w", VoiceCuesFile, unmarshalErr)
		}
		sess.VoiceCues = voiceCues
	case !os.IsNotExist(voiceErr):
		return director.Project{}, fmt.Errorf("could not read %s: %w", VoiceCuesFile, voiceErr)
	}

	project := director.Direct(sess)

	buf, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return director.Project{}, fmt.Errorf("could not marshal project: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), append(buf, '\n'), 0o600); err != nil {
		return director.Project{}, fmt.Errorf("write %s: %w", ProjectFile, err)
	}
	return project, nil
}

// Render ensures dir has a validated raw.mp4 (building or recovering it from frames/segments
// if needed — see internal/plate.EnsureRaw, which also compacts those sources once raw.mp4 is
// confirmed usable), heals session.json's events/network from its journal if a hard kill left
// them empty (session.Recover), opportunistically runs the voice stage, (re-)analyzes if
// project.json is missing or older than voice.json, and renders demo.mp4.
func Render(ctx context.Context, dir string, opts Options) (render.Result, error) {
	if _, err := plate.EnsureRaw(dir, opts.Logf); err != nil {
		return render.Result{}, fmt.Errorf("ensure raw plate: %w", err)
	}
	// Best-effort and silent-if-nothing-to-do: session.Recover itself is a no-op (false, nil)
	// whenever session.json already has real events/network, which is the overwhelming common
	// case — a normal recording's own Finalize/Abort already wrote them.
	if healed, err := session.Recover(dir); err != nil {
		opts.logf("session: could not recover events/network from the journal: %s", err)
	} else if healed {
		opts.logf("session: recovered events/network from the journal (session.json had none of its own)")
	}

	// Freshly written voice.json invalidates any project.json already on disk (it was
	// analyzed before narration existed, or before a later manual voice re-run rewrote it), so
	// analyze must re-run even on a second Render of the same session — not just the "no
	// project.json yet" case below. voiceJustProcessed alone would miss the latter (voice.json
	// existing already makes runVoiceStage a no-op), so it's OR'd with an mtime check that
	// catches project.json having been generated before voice.json's last write, regardless of
	// which process wrote either one or when.
	voiceJustProcessed := opts.runVoiceStage(ctx, dir)
	voiceStale := voiceJustProcessed || projectPredatesVoiceJSON(dir)

	var project director.Project
	projectPath := filepath.Join(dir, ProjectFile)
	buf, readErr := os.ReadFile(projectPath) //nolint:gosec // G304
	if readErr == nil && !voiceStale {
		if err := json.Unmarshal(buf, &project); err != nil {
			return render.Result{}, fmt.Errorf("could not parse %s: %w", ProjectFile, err)
		}
	} else {
		reason := "no project.json yet"
		if voiceStale {
			reason = "voice narration is ready"
		}
		opts.logf("%s, analyzing the session", reason)
		var err error
		project, err = Analyze(dir)
		if err != nil {
			return render.Result{}, err
		}
	}

	result, err := render.Project(dir, project, opts.Logf)
	if err != nil {
		return render.Result{}, fmt.Errorf("render project: %w", err)
	}
	return result, nil
}

// runVoiceStage attempts o.Voice when dir has narration waiting to be processed, and reports
// whether it wrote a fresh voice.json. Every way this can be skipped or fail is logged and
// non-fatal: voice is opt-in and additive, so a session recorded without narration tooling
// configured — or one whose narration pipeline fails outright — must still render, just
// without narration.
func (o Options) runVoiceStage(ctx context.Context, dir string) bool {
	if o.Voice == nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, session.VoiceFile)); os.IsNotExist(err) { //nolint:gosec // G703
		return false // no narration was recorded — the pre-feature, byte-identical path
	}
	if _, err := os.Stat(filepath.Join(dir, VoiceCuesFile)); err == nil { //nolint:gosec // G703
		return false // already processed, by an earlier render or a manual voice run
	}

	ran, err := o.Voice(ctx, dir)
	if err != nil {
		o.logf("voice: %s — rendering without narration (retry with `take5 voice %s`)", err, dir)
		return false
	}
	return ran
}

// projectPredatesVoiceJSON reports whether project.json is older than voice.json — meaning it
// was generated before voice.json's last write and so is missing (or has stale) narration
// cues, most commonly after a manual forced voice re-run on a session that was already
// rendered once. Either file missing means "nothing to compare, not stale": a missing
// voice.json is the normal no-narration case, and a missing project.json is already handled by
// Render's own read-error branch.
func projectPredatesVoiceJSON(dir string) bool {
	voiceInfo, err := os.Stat(filepath.Join(dir, VoiceCuesFile)) //nolint:gosec // G703
	if err != nil {
		return false
	}
	projectInfo, err := os.Stat(filepath.Join(dir, ProjectFile)) //nolint:gosec // G703
	if err != nil {
		return false
	}
	return projectInfo.ModTime().Before(voiceInfo.ModTime())
}

// Every render-side failure this binary can report is written with a "render: " marker
// somewhere on its own line in debug.log — the render CLI command writes it directly to
// stderr; a detached-spawn failure is appended after a timestamp instead — so the marker is
// matched anywhere in the line, not just at its start.
const renderFailureMarker = "render: "

// internal/session.Writer.Log's "aborted: <reason>" line, written before session.json exists
// with a non-zero duration but no completed recording.
const recordingFailureMarker = "aborted: "

// Status derives a session's pipeline status ("ready", "processing", "render_failed",
// "recording_failed") from the files render and the recording lifecycle leave behind in dir —
// the render process (spawned detached from the host) has no channel back to report status
// directly. For a failure, reason is the one-line message pulled from debug.log.
func Status(dir string) (status, reason string) {
	// dir is one of the output directory's own subdirectories, listed by a caller like
	// internal/host.ListSessions, not attacker-controlled input.
	if _, err := os.Stat(filepath.Join(dir, OutputFile)); err == nil { //nolint:gosec // G703
		return "ready", ""
	}
	log, err := os.ReadFile(filepath.Join(dir, DebugLogFile)) //nolint:gosec // G304
	if err != nil {
		return "processing", ""
	}
	text := string(log)
	if r, ok := lastLineFragment(text, renderFailureMarker); ok {
		return "render_failed", r
	}
	if r, ok := lastLineFragment(text, recordingFailureMarker); ok {
		return "recording_failed", r
	}
	return "processing", ""
}

// lastLineFragment returns the text following the last occurrence of marker up to the end of
// that line, trimmed. Used to pull a one-line human-readable reason out of debug.log without
// needing a stricter, more brittle log format.
func lastLineFragment(text, marker string) (string, bool) {
	idx := strings.LastIndex(text, marker)
	if idx == -1 {
		return "", false
	}
	rest := text[idx+len(marker):]
	if nl := strings.IndexByte(rest, '\n'); nl != -1 {
		rest = rest[:nl]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}
