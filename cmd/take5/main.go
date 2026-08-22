// take5 — the local process Chrome talks to over native messaging, and everything
// it does after a recording ends. See docs/plans/go-port.md and README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"take5/internal/config"
	"take5/internal/host"
	"take5/internal/install"
	"take5/internal/postproduction"
	"take5/internal/render"
	"take5/internal/transcribe"
	"take5/internal/voiceover"
)

const usage = `take5 — record a browser walkthrough, get a polished demo

  take5 host [--out <dir>]
      Native-messaging host. Spawned by Chrome, not run by hand: reads session-start,
      frame, event, network and session-stop messages from stdin, writes each completed
      session to <dir>, and post-produces it automatically.

      Chrome invokes the manifest's "path" directly and cannot prepend a subcommand — it
      always calls "<path> chrome-extension://<id>/". This binary detects that shape (an
      argv[1] starting with "chrome-extension://") and runs as if "host" had been given, so
      install can register this same executable as-is.

  take5 install [-key <base64>]
      Registers this binary as the native-messaging host, deriving the extension id from
      extension/manifest.json's "key" field (or -key, if given).

  take5 doctor
      Re-checks everything install depends on: this binary's own path, ffmpeg/ffprobe on
      PATH, and every installed host manifest.

  take5 render <session-dir>
      Re-render demo.mp4 from an existing recording + project.json. If the session has a
      voice.webm and no voice.json yet, runs the voice stage first (see "voice" below) —
      opportunistically: missing OPENROUTER_API_KEY or a whisper.cpp model just renders
      without narration rather than failing. Does not talk to Chrome.

  take5 analyze <session-dir>
      Regenerate project.json from session.json without rendering. Does not run the voice
      stage — pass a session that already has voice.json if you want its cues picked up.

  take5 voice <session-dir> [-openrouter-key <key>] [-openrouter-model <id>]
                       [-voice <name>] [-language <code>]
      Opt-in voice-annotation stage: transcribes voice.webm locally (whisper.cpp), sends only
      the cleaned-up text to an LLM for rewrite, synthesizes narration via TTS, and writes
      voice.json + voice/*.mp3 next to session.json. Requires whisper-cli, a ggml model
      (see "take5 doctor"), an OpenRouter API key, and edge-tts. The key can be
      OPENROUTER_API_KEY, but Chrome-launched processes often can't see shell environment
      variables at all — set it via -openrouter-key, or put "openrouterApiKey" in
      ~/.config/take5/config.json instead. If this network needs a proxy to reach
      OpenRouter at all (an interactive shell's HTTPS_PROXY doesn't reach a Chrome-launched
      process either, same problem as the key), put its http:// or https:// URL in that same
      file's "proxy" — see "take5 doctor"'s "proxy:" line. Does not talk to Chrome;
      does not touch demo.mp4 — run analyze/render afterward to pick voice.json up.
      Normally you don't need to run this by hand: "render" (and so the automatic
      post-recording pipeline) already does it whenever it finds a voice.webm to process.
      Use this directly to pick different flags, or to pre-process without rendering yet.

  take5 setup-voice
      Best-effort, idempotent: checks the same dependencies "voice" needs (whisper.cpp model,
      edge-tts) and installs whichever are missing — downloads a ggml model to
      ~/.config/take5/models if none is found, and installs edge-tts via pipx if it
      isn't on PATH. Never fails the run; each check that can't be fixed automatically (no
      whisper-cli, no pipx, no OpenRouter key — that one has to come from you) just prints
      what to do by hand. Safe to re-run; already-satisfied checks are no-ops.

  take5 version
      Print the build version.
`

// version is set at build time via -ldflags "-X main.version=...", e.g. by GoReleaser
// (.goreleaser.yaml). Left as "dev" for a plain `go build`.
var version = "dev"

func main() {
	applyProxyConfigFallback()

	if len(os.Args) < 2 {
		fmt.Print(usage)
		return
	}

	// See the "host" usage note above: this is the shape Chrome actually invokes.
	if strings.HasPrefix(os.Args[1], "chrome-extension://") {
		cmdHost(os.Args[1:])
		return
	}

	switch os.Args[1] {
	case "host":
		cmdHost(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	case "doctor":
		cmdDoctor(os.Args[2:])
	case "render":
		cmdRender(os.Args[2:])
	case "analyze":
		cmdAnalyze(os.Args[2:])
	case "voice":
		cmdVoice(os.Args[2:])
	case "setup-voice":
		cmdSetupVoice(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

// cmdHost never writes to stdout except through host.Run's framed messages — stdout *is*
// the protocol (SPIKE.md §1 consequences) — so every log line goes to stderr.
func cmdHost(args []string) {
	fs := flag.NewFlagSet("host", flag.ExitOnError)
	// Chrome spawns this process with an unpredictable working directory — never the
	// user's, since no shell is involved — so unlike the Node CLI's `start`, a relative
	// default here would land somewhere the user cannot find it. "" means "unset";
	// defaultOutputDir() resolves it against $HOME instead of cwd.
	out := fs.String("out", "", "output directory for recordings (default: ~/take5-output)")
	_ = fs.Parse(args)

	// stderr alone for these two: nothing durable exists to write to yet (outputDir isn't
	// resolved, or couldn't be created), and both are rare bootstrap failures a user debugging
	// by hand (running this command directly in a terminal, not through Chrome) sees directly.
	bootLogger := log.New(os.Stderr, "", log.LstdFlags)

	target := *out
	if target == "" {
		target = defaultOutputDir()
	}
	outputDir, err := filepath.Abs(target)
	if err != nil {
		bootLogger.Fatalf("resolve output dir: %v", err)
	}
	if err = os.MkdirAll(outputDir, 0o750); err != nil { // #nosec G301
		bootLogger.Fatalf("create output dir: %v", err)
	}

	// Chrome never lets the user see this process's stderr (SPIKE.md §5) — a fact repeated at
	// every prior site that already had to work around it (session.Writer's per-recording
	// debug.log, appendDebugLog below). logDurable below mirrors every line here into the
	// current session's debug.log as soon as one exists (host.OnSessionStart fires the moment
	// session.Open returns, before any frame/event is written) — there is deliberately no
	// separate host.log: a caller-origin mismatch or startup failure before a session exists
	// has nowhere durable to go and stays stderr-only, which is fine since nothing has
	// happened yet for a user to need to recover.
	logger := log.New(os.Stderr, "", log.LstdFlags)

	var dirMu sync.Mutex
	var activeDir string
	setActiveDir := func(dir string) {
		dirMu.Lock()
		activeDir = dir
		dirMu.Unlock()
	}
	logDurable := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		logger.Print(msg)
		dirMu.Lock()
		dir := activeDir
		dirMu.Unlock()
		if dir != "" {
			appendDebugLog(dir, msg)
		}
	}

	// Chrome passes the calling origin as the first extra argv entry
	// (chrome-extension://<id>/); install's manifest already restricts who can spawn this
	// process at all, so a mismatch here would mean the manifest and the binary disagree.
	var callerOrigin string
	if fs.NArg() > 0 {
		callerOrigin = fs.Arg(0)
	}

	opts := host.Options{
		OutputDir:      outputDir,
		CallerOrigin:   callerOrigin,
		Logf:           logDurable,
		OnSessionStart: setActiveDir,
		OnComplete: func(dir string) {
			logDurable("recording complete: %s", dir)
			if startErr := spawnDetachedRender(dir); startErr != nil {
				// This is the one failure that happens after the session.Writer's own
				// debug.log has already been closed — without logDurable's mirroring here,
				// the session directory would sit at "processing" forever with no trace of
				// why anywhere on disk.
				logDurable("render: could not start post-production: %v (retry with: take5 render %s)", startErr, dir)
				return
			}
			logDurable("post-production started independently of this process; see %s/debug.log", dir)
		},
		OnFailed: func(dir, reason string) {
			logDurable("recording failed (%s). partial data kept at %s", reason, dir)
		},
	}

	// A production recording was lost with no session.json and no "aborted:" line in
	// debug.log at all — nothing in Run's own graceful-shutdown path (EOF on stdin) had a
	// chance to run, which points at the process being killed outright. Go's default
	// disposition for SIGTERM is to exit immediately with zero cleanup, and cmd/spike-host's
	// earlier research (SPIKE.md) already flagged SIGTERM/SIGINT/SIGHUP as signals Chrome or
	// the OS can plausibly send during teardown — this host command itself never caught them
	// until now. Closing stdin from here unblocks host.Run's blocked read with os.ErrClosed,
	// which Run now treats the same as a normal EOF: Abort runs, session.json gets written,
	// and only then does this process actually exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		s := <-sig
		logDurable("received signal %s, shutting down gracefully", s)
		_ = os.Stdin.Close()
	}()

	// Even with the signal handler above, two real recordings still died with nothing logged
	// past "recording -> ...": no signal caught, no panic, no "aborted:" line — which is what
	// a graceful EOF-on-stdin disconnect would have produced. That rules out both paths this
	// file already handles, meaning something else ends the process outright. A heartbeat
	// can't prevent that, but it turns "silence, then gone" into "alive as of the last
	// heartbeat before TIME X" — the only way past guessing without being able to attach a
	// debugger to a process an external kill has already reaped.
	heartbeat := time.NewTicker(5 * time.Second)
	go func() {
		for range heartbeat.C {
			logDurable("alive, pid=%d", os.Getpid())
		}
	}()

	err = runHost(logDurable, opts)
	heartbeat.Stop()
	if err != nil {
		os.Exit(1)
	}
}

// runHost recovers a panic anywhere in host.Run's synchronous message-processing path — every
// package that path calls into (internal/host, internal/session, internal/plate) — so it
// leaves a stack trace in the current session's debug.log (via logDurable) instead of dying
// completely silently: an unrecovered panic still exits non-zero, exactly as it would have,
// but now there is something to look at afterward. It cannot save the recording in progress
// (the panic already unwound past whatever session state existed); it only makes the failure
// diagnosable. The panic path calls os.Exit directly (from inside the recover, not around it)
// since by then every other defer has already run; a plain Run error instead returns to the
// caller, which has no defers of its own to worry about skipping.
func runHost(logDurable func(format string, a ...any), opts host.Options) error {
	defer func() {
		if r := recover(); r != nil {
			logDurable("host: panic: %v\n%s", r, debug.Stack())
			os.Exit(1)
		}
	}()
	if err := host.Run(os.Stdin, os.Stdout, opts); err != nil {
		logDurable("host: %v", err)
		return fmt.Errorf("host: %w", err)
	}
	return nil
}

// appendDebugLog writes one timestamped line to a session's debug.log, the durable record a
// user (or internal/host.ListSessions) can actually inspect — unlike this process's own
// stderr, which nothing captures once Chrome has spawned it. Failures are swallowed: a
// logging failure must never be the reason a caller thinks it needs to report a second error.
// timestampLine prefixes line with a UTC RFC3339Nano timestamp — the convention
// internal/session.Writer.Log already uses for session.json's sibling debug.log, so every
// line debug.log ever receives (that writer's session-lifecycle events, and this file's own
// render/voice pipeline progress via timestampedLogf below) can be correlated by time from
// one place instead of only the first line in the file having one.
func timestampLine(line string) string {
	return time.Now().UTC().Format(time.RFC3339Nano) + " " + line
}

// timestampedLogf returns a logf-shaped function (the signature postproduction.Render and its
// own callees already take) that timestamps every line it writes to w, per timestampLine.
func timestampedLogf(w io.Writer) func(format string, a ...any) {
	return func(format string, a ...any) {
		fmt.Fprintln(w, timestampLine(fmt.Sprintf(format, a...)))
	}
}

func appendDebugLog(dir, line string) {
	// dir is a session directory this same process created moments earlier (OnComplete
	// fires with the dir session.Open returned), not attacker-controlled input.
	f, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, timestampLine(line))
}

// spawnDetachedRender is spec 26's automatic path. Chrome tears down this process's own
// process group shortly after the port disconnects — well before post-production (minutes of
// FFmpeg work over hundreds of frames) can finish — so post-production cannot run inline here.
// Instead this starts a second, independent `take5 render <dir>` process and returns
// immediately without waiting on it: detachedSysProcAttr puts it in its own session/process
// group so it keeps running after this one is killed. `take5 render <dir>` is also the
// manual recovery path if it fails partway.
func spawnDetachedRender(dir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve own executable: %w", err)
	}

	logFile, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G302 G304
	if err != nil {
		return fmt.Errorf("open debug.log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "render", dir) // #nosec G204
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start render process: %w", err)
	}
	return nil
}

// defaultOpenRouterModel is shared between autoVoiceStage's unattended run and cmdVoice's own
// -openrouter-model flag default, so an automatic render and a manual `take5 voice`
// pick the same model unless a human overrides the flag by hand. There's no equivalent
// default-voice constant: leaving Options.Voice unset lets voiceover.Run pick a voice from
// whisper.cpp's own detected language (voiceover.DefaultVoiceForLanguage) instead of always
// narrating in English regardless of what language was actually spoken.
const defaultOpenRouterModel = "deepseek/deepseek-chat"

// configPath is config.Path(), with the (very unlikely — os.UserHomeDir() failing) error
// swallowed to "": every call site here already treats an empty path as "can't tell you
// where, sorry" rather than a reason to fail whatever it's in the middle of doing.
func configPath() string {
	path, err := config.Path()
	if err != nil {
		return ""
	}
	return path
}

// openRouterAPIKey resolves the key the same way for cmdVoice's -openrouter-key flag default
// and autoVoiceStage's unattended run: the env var first (so an explicit `export` or a
// terminal-launched Chrome still wins), then config.Config's OpenRouterAPIKey.
func openRouterAPIKey() string {
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		return key
	}
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	return cfg.OpenRouterAPIKey
}

// applyProxyConfigFallback sets HTTPS_PROXY (and HTTP_PROXY, for parity) in this process's own
// environment from config.Config's Proxy when neither is already set. Called once,
// unconditionally, at the very top of main(): net/http.ProxyFromEnvironment (what
// http.DefaultTransport already uses, unmodified, for every *http.Client in this codebase,
// including internal/voiceover's OpenRouter rewrite client) reads HTTPS_PROXY directly, so
// setting it here is enough to fix every HTTP call in the process — and every exec.Command
// subprocess (edge-tts included) inherits the same environment for free, without each one
// needing its own proxy-resolution logic.
//
// Exists because a process Chrome spawns (native messaging -> host -> render, detached)
// doesn't inherit the launching shell's environment, proxy settings included — confirmed
// directly while debugging the OpenRouter rewrite step's "Access denied by security policy"
// 403s: they reproduce on-demand by unsetting HTTPS_PROXY for an otherwise-identical manual
// request, and stop when it's set again. This network's direct, unproxied path to OpenRouter is
// what's actually being blocked (real Cloudflare response headers throughout, not a local
// interceptor), not anything about the request itself.
//
// Go's net/http only reads HTTP_PROXY/HTTPS_PROXY, never ALL_PROXY, and only understands
// http:// and https:// proxy URLs — no built-in SOCKS5 support. So the "already set" check below
// only looks at the scheme-specific vars: ALL_PROXY (a SOCKS5 proxy for every other tool on the
// machine, say) has no effect on this process's HTTP calls at all, so its presence must not
// suppress config.Config's Proxy — the one setting that actually can reach this codebase's
// http.Client. An ALL_PROXY-only shell with no HTTPS_PROXY was exactly the case that made
// `doctor` misreport "none (direct connection)" despite a working proxy sitting unused in
// config.json.
func applyProxyConfigFallback() {
	for _, envVar := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if os.Getenv(envVar) != "" {
			return
		}
	}
	cfg, err := config.Load()
	if err != nil || cfg.Proxy == "" {
		return
	}
	_ = os.Setenv("HTTPS_PROXY", cfg.Proxy)
	_ = os.Setenv("HTTP_PROXY", cfg.Proxy)
}

// newVoiceOptions builds the voiceover.Options both cmdVoice's explicit run and
// autoVoiceStage's opportunistic one wire the same way — only the credentials/model/flags
// differ between an unattended render and a human running `take5 voice` by hand.
func newVoiceOptions(key, model, ttsVoice, language, modelPath string) voiceover.Options {
	return voiceover.Options{
		Rewriter: voiceover.NewOpenAICompatRewriter(voiceover.OpenAICompatConfig{
			BaseURL: "https://openrouter.ai/api/v1",
			APIKey:  key,
			Model:   model,
		}),
		TTS:       voiceover.NewEdgeTTSProvider(),
		Voice:     ttsVoice,
		ModelPath: modelPath,
		Language:  language,
	}
}

// autoVoiceStage returns a postproduction.VoiceStage that opportunistically resolves
// narration credentials and runs the voice pipeline (transcribe -> LLM rewrite -> TTS), for
// wiring into postproduction.Render's automatic path — so a recording made with narration on
// comes out of the automatic post-recording pipeline already narrated, no separate manual
// `take5 voice <dir>` required.
//
// Every way this can be unavailable is logged and reported as "nothing to try" (false, nil),
// never an error: voice is opt-in and additive (docs/plans/2026-08-19-voice-annotations.md's
// own framing), so a session recorded without OPENROUTER_API_KEY configured, or without
// whisper.cpp installed, must still render — just without narration, exactly as if the
// extension's voice checkbox had been off. postproduction.Render treats an actual pipeline
// error from Run the same way — logged, non-fatal — so it isn't duplicated here.
func autoVoiceStage(logf func(format string, args ...any)) postproduction.VoiceStage {
	return func(ctx context.Context, dir string) (bool, error) {
		openRouterKey := openRouterAPIKey()
		if openRouterKey == "" {
			logf("voice: no OpenRouter key (OPENROUTER_API_KEY, or \"openrouterApiKey\" in %s) — rendering without narration (configure one, then run `take5 voice %s`)", configPath(), dir)
			return false, nil
		}
		modelPath, ok := transcribe.ModelPath()
		if !ok {
			logf("voice: no whisper.cpp model found — rendering without narration (run `take5 setup-voice`)")
			return false, nil
		}

		logf("voice: narration was recorded, transcribing + rewriting + synthesizing before render")
		// Voice left unset on purpose — Run picks a language-appropriate one once it knows
		// what whisper.cpp actually detected, which an unattended run has no other way to
		// predict ahead of time.
		opts := newVoiceOptions(openRouterKey, defaultOpenRouterModel, "", "", modelPath)
		if _, err := voiceover.Run(ctx, dir, opts); err != nil {
			return false, fmt.Errorf("voice pipeline: %w", err)
		}
		return true, nil
	}
}

func cmdAnalyze(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "analyze needs a session directory")
		os.Exit(1)
	}
	dir := args[0]
	project, err := postproduction.Analyze(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"Analyzed %s: %d timeline segments, %d ms output, %d camera keyframes, %d annotations, %d voice cue(s)\n",
		dir, len(project.Timeline), project.DurationMs, len(project.Camera), len(project.Annotations), len(project.Voice),
	)
}

// parsePositionalFlags parses args against fs, tolerating one positional argument on either
// side of the flags (or interspersed between value-taking ones) rather than only before them.
// flag.FlagSet.Parse stops at the first non-flag token and treats everything after as
// positional, so `voice <dir> -force` otherwise leaves -force unparsed and silently false —
// the documented invocation puts the directory first. Repeated Parse calls are safe: flag
// definitions don't change between them, and each call only processes the args it's given.
func parsePositionalFlags(fs *flag.FlagSet, args []string) string {
	positional := ""
	for {
		_ = fs.Parse(args)
		remaining := fs.Args()
		if len(remaining) == 0 {
			break
		}
		if positional == "" {
			positional = remaining[0]
		}
		args = remaining[1:]
	}
	return positional
}

// cmdVoice and cmdSetupVoice are the only subcommands that touch the network or spawn
// whisper.cpp — see docs/plans/2026-08-19-voice-annotations.md's pipeline-placement
// decision. Everything cmdVoice needs (API key, model, voice) is a flag or env var; nothing
// is hard-coded, since the production TTS/LLM provider choice is an explicit open question
// in that plan.
func cmdVoice(args []string) {
	fs := flag.NewFlagSet("voice", flag.ExitOnError)
	openRouterKey := fs.String("openrouter-key", openRouterAPIKey(), "OpenRouter API key for transcript rewriting (or set OPENROUTER_API_KEY, or put \"openrouterApiKey\" in "+configPath()+")")
	openRouterModel := fs.String("openrouter-model", defaultOpenRouterModel, "OpenRouter model id for transcript rewriting (spike-validated default)")
	ttsVoice := fs.String("voice", "", "edge-tts voice name (default: picked from whisper.cpp's detected language)")
	language := fs.String("language", "", "whisper.cpp + rewrite language code (default: auto-detect)")
	force := fs.Bool("force", false, "redo the rewrite+TTS pipeline even if voice.json already exists")
	dir := parsePositionalFlags(fs, args)

	if dir == "" {
		fmt.Fprintln(os.Stderr, "voice needs a session directory")
		os.Exit(1)
	}

	// Unlike analyze/render (free, local, `-y`-safe to redo), a re-run here means a fresh
	// LLM + TTS bill — so "safe to re-run" defaults to a no-op instead of `render`'s always-
	// regenerate behavior, matching this task's own framing ("either no-op or explicit
	// --force"). -force opts back into always-redo.
	if !*force {
		if _, err := os.Stat(filepath.Join(dir, postproduction.VoiceCuesFile)); err == nil {
			fmt.Printf("voice: %s already exists, skipping (pass -force to redo)\n", filepath.Join(dir, postproduction.VoiceCuesFile))
			return
		}
	}

	if *openRouterKey == "" {
		fmt.Fprintf(os.Stderr, "voice: no OpenRouter API key (-openrouter-key, OPENROUTER_API_KEY, or \"openrouterApiKey\" in %s)\n", configPath())
		os.Exit(1)
	}
	modelPath, ok := transcribe.ModelPath()
	if !ok {
		fmt.Fprintln(os.Stderr, "voice: no whisper.cpp model found. Run `take5 setup-voice`, or set WHISPER_MODEL_PATH yourself.")
		os.Exit(1)
	}
	if !render.IsFfmpegAvailable() {
		fmt.Fprintln(os.Stderr, "voice: ffmpeg was not found (needed to transcode voice.webm to WAV). Install it and try again.")
		os.Exit(1)
	}

	opts := voiceover.Options{
		Rewriter: voiceover.NewOpenAICompatRewriter(voiceover.OpenAICompatConfig{
			BaseURL: "https://openrouter.ai/api/v1",
			APIKey:  *openRouterKey,
			Model:   *openRouterModel,
		}),
		TTS:       voiceover.NewEdgeTTSProvider(),
		Voice:     *ttsVoice,
		ModelPath: modelPath,
		Language:  *language,
	}

	cues, err := voiceover.Run(context.Background(), dir, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice: %v\n", err)
		os.Exit(1)
	}
	kept := 0
	for _, c := range cues {
		if c.RewrittenText != nil {
			kept++
		}
	}
	fmt.Printf("voice: %d segments transcribed, %d kept, %d dropped -> %s\n",
		len(cues), kept, len(cues)-kept, filepath.Join(dir, postproduction.VoiceCuesFile))
}

// whisperModelURL points at the GGML model repo brew's own `whisper-cpp` formula caveats
// name as the place to get one from (`brew info whisper-cpp`). Deliberately the multilingual
// ggml-base.bin, not the English-only ggml-base.en.bin the naming convention makes tempting to
// reach for first: whisper.cpp's own auto-detect (buildArgs' `-l auto` default) needs a
// multilingual model to mean anything — fed to the .en model, non-English narration doesn't
// transcribe into wrong text, it transcribes into the literal placeholder
// "(speaking in foreign language)" for every segment, which the rewrite stage then correctly
// drops as filler, silently producing zero voice cues. WHISPER_MODEL_PATH (or a model dropped
// in one of transcribe.ManagedModelDir's sibling directories) still overrides this at
// resolution time, for anyone who genuinely only ever narrates in English and wants the
// smaller/faster .en variant instead.
const whisperModelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin"

func downloadFile(url, dest string) error {
	// url is this file's own constant, not a variable built from user input.
	resp, err := http.Get(url) //nolint:gosec // G107
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}

	// Downloaded to a .tmp sibling first and renamed into place, so a connection dropped
	// partway through never leaves a truncated file at dest for ModelPath to find and hand
	// whisper-cli next time.
	tmp := dest + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec // G304: dest is this call's own construction
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, dest, err)
	}
	return nil
}

func downloadWhisperModel() (string, error) {
	dir, err := transcribe.ManagedModelDir()
	if err != nil {
		return "", fmt.Errorf("resolve managed model dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	dest := filepath.Join(dir, filepath.Base(whisperModelURL))
	if err := downloadFile(whisperModelURL, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// installEdgeTTS shells out to pipx (a pip3 install falls under the same PATH problem
// everything else here works around, and pipx's isolated venv is the better-behaved install
// anyway) and then records where it landed — ~/.local/bin, not one of EdgeTTSBin's
// extraBinDirs — as config.Config's EdgeTTSPath, so EdgeTTSBin finds it on every future call
// without needing PATH or an environment variable Chrome won't forward.
func installEdgeTTS() (string, error) {
	pipx, err := exec.LookPath("pipx")
	if err != nil {
		return "", fmt.Errorf("pipx not found — install it yourself (e.g. `brew install pipx`), or install edge-tts yourself (`pip3 install --user edge-tts`) and re-run")
	}

	// pipx is a resolved, absolute path from LookPath above; "install"/"edge-tts" are
	// constants — nothing here is attacker-controlled input.
	cmd := exec.Command(pipx, "install", "edge-tts") //nolint:gosec // G204
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		return "", fmt.Errorf("pipx install edge-tts: %w", runErr)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	installedPath := filepath.Join(home, ".local", "bin", "edge-tts")
	if _, err := os.Stat(installedPath); err != nil {
		return "", fmt.Errorf("pipx install edge-tts succeeded but %s wasn't there afterward: %w", installedPath, err)
	}

	if err := config.Update(func(c *config.Config) { c.EdgeTTSPath = installedPath }); err != nil {
		return "", fmt.Errorf("record edge-tts's path in %s: %w", configPath(), err)
	}
	return installedPath, nil
}

// cmdSetupVoice is best-effort and idempotent by design (see its own usage text): every
// check that can be fixed automatically is; every one that can't (no whisper-cli, no pipx, no
// OpenRouter key — a secret only the operator can obtain) prints what to do by hand instead
// of failing the run, the same "additive, never fatal" posture autoVoiceStage takes at render
// time. Reuses internal/install.Doctor's own checks rather than re-implementing them, so this
// and `take5 doctor` never disagree about what's already present.
func cmdSetupVoice(args []string) {
	fs := flag.NewFlagSet("setup-voice", flag.ExitOnError)
	_ = fs.Parse(args)

	report := install.Doctor("") // the extension key isn't relevant to any check used below

	if report.WhisperFound {
		fmt.Printf("whisper-cli:   already found at %s\n", report.WhisperPath)
	} else {
		fmt.Println("whisper-cli:   not found — install it yourself (e.g. `brew install whisper-cpp`) and re-run")
	}

	if report.WhisperModelFound {
		fmt.Printf("whisper model: already found at %s\n", report.WhisperModelPath)
	} else {
		fmt.Println("whisper model: downloading a default model (this can take a while)...")
		if path, err := downloadWhisperModel(); err != nil {
			fmt.Printf("whisper model: could not download one: %v\n", err)
		} else {
			fmt.Printf("whisper model: downloaded to %s\n", path)
		}
	}

	if report.EdgeTTSFound {
		fmt.Printf("edge-tts:      already found at %s\n", report.EdgeTTSPath)
	} else {
		fmt.Println("edge-tts:      installing via pipx...")
		if path, err := installEdgeTTS(); err != nil {
			fmt.Printf("edge-tts:      could not install it: %v\n", err)
		} else {
			fmt.Printf("edge-tts:      installed to %s\n", path)
		}
	}

	if openRouterAPIKey() != "" {
		fmt.Println("openrouter key: already configured")
	} else {
		fmt.Printf("openrouter key: not found — get one at https://openrouter.ai/keys and put it in %s as \"openrouterApiKey\"\n", configPath())
	}
}

func cmdRender(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "render needs a session directory")
		os.Exit(1)
	}
	dir := args[0]
	if !render.IsFfmpegAvailable() {
		// "render: " prefixed like every other failure this command can report (see the
		// err != nil branch below), so internal/host.ListSessions recognizes it in debug.log
		// the same way regardless of which check failed.
		fmt.Fprintln(os.Stderr, "render: ffmpeg was not found. Install it (e.g. `brew install ffmpeg`) and try again.")
		os.Exit(1)
	}
	logf := timestampedLogf(os.Stdout)
	result, err := postproduction.Render(context.Background(), dir, postproduction.Options{
		Logf:  logf,
		Voice: autoVoiceStage(logf),
	})
	if err != nil {
		errLogf := timestampedLogf(os.Stderr)
		errLogf("render: %v", err)
		var ferr *render.FfmpegError
		if errors.As(err, &ferr) && ferr.Stderr != "" {
			errLogf("%s", strings.TrimSpace(ferr.Stderr))
		}
		os.Exit(1)
	}
	logf("Demo ready: %s", result.Output)
}

func defaultOutputDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "take5-output")
	}
	return "demo-output"
}

func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	key := fs.String("key", "", `extension manifest key (base64 DER SPKI); defaults to extension/manifest.json's "key" field`)
	manifestPath := fs.String("extension-manifest", "extension/manifest.json", "path to the extension manifest, when -key is not given")
	_ = fs.Parse(args)

	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: could not resolve this binary's own path: %v\n", err)
		os.Exit(1)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		os.Exit(1)
	}

	base64Key := *key
	if base64Key == "" {
		base64Key, err = install.ReadExtensionKey(*manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "install: %v\n", err)
			os.Exit(1)
		}
	}

	written, err := install.Install(execPath, base64Key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		os.Exit(1)
	}
	for _, m := range written {
		fmt.Printf("registered for %s: %s\n", m.Browser, m.Path)
	}
}

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	key := fs.String("key", "", `extension manifest key (base64 DER SPKI); defaults to extension/manifest.json's "key" field`)
	manifestPath := fs.String("extension-manifest", "extension/manifest.json", "path to the extension manifest, when -key is not given")
	_ = fs.Parse(args)

	base64Key := *key
	if base64Key == "" {
		if k, err := install.ReadExtensionKey(*manifestPath); err == nil {
			base64Key = k
		}
	}

	report := install.Doctor(base64Key)

	fmt.Printf("this binary:   %s\n", presence(report.ExecPath, report.ExecExists))
	fmt.Printf("ffmpeg:        %s\n", presence(report.FfmpegPath, report.FfmpegFound))
	fmt.Printf("ffprobe:       %s\n", presence(report.FfprobePath, report.FfprobeFound))
	fmt.Printf("whisper-cli:   %s (only needed for voice annotations)\n", presence(report.WhisperPath, report.WhisperFound))
	fmt.Printf("whisper model: %s\n", presenceOrFix(report.WhisperModelPath, report.WhisperModelFound))
	fmt.Printf("edge-tts:      %s\n", presenceOrFix(report.EdgeTTSPath, report.EdgeTTSFound))
	// Value never printed — this is a secret, unlike the other checks above.
	if openRouterAPIKey() != "" {
		fmt.Println("openrouter key: configured (only needed for voice annotations)")
	} else {
		fmt.Printf("openrouter key: not found — set OPENROUTER_API_KEY or put \"openrouterApiKey\" in %s (only needed for voice annotations)\n", configPath())
	}
	// applyProxyConfigFallback already ran (main's first line), so this reflects what's
	// actually active for this run, not just what's on disk — real env var or the config
	// file's, either way. Printed (unlike the key above): a proxy URL isn't a secret, and
	// seeing exactly which one is in effect is the point of this check.
	if p := os.Getenv("HTTPS_PROXY"); p != "" {
		fmt.Printf("proxy:         %s\n", p)
	} else {
		fmt.Printf("proxy:         none (direct connection) — if voice narration gets a 403 from OpenRouter, this network may need one; put its URL in %s as \"proxy\"\n", configPath())
	}
	if base64Key == "" {
		fmt.Println("extension key: none found — pass -key or add one to the extension manifest")
	}
	fmt.Println("host manifests:")
	for _, m := range report.Manifests {
		status := "ok"
		if m.Issue != "" {
			status = m.Issue
		}
		fmt.Printf("  %-14s %s (%s)\n", m.Browser, m.Path, status)
	}
}

func presence(path string, found bool) string {
	if found {
		return path
	}
	return "not found"
}

// presenceOrFix is presence for the two checks `take5 setup-voice` can actually
// remedy on its own (unlike the extension key or a missing whisper-cli, which need a human).
func presenceOrFix(path string, found bool) string {
	if found {
		return path
	}
	return "not found (only needed for voice annotations — run `take5 setup-voice`)"
}
