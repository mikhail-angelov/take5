# take5

<!-- Keep this file current: when a convention changes, it changes in the same commit. -->

## Project overview

A Chrome extension (MV3) plus a local Go binary. The extension records a real walkthrough of
a web app over the DevTools protocol and hands the raw frames/events to the binary via native
messaging; the binary turns that into a polished demo.mp4 with no manual editing. See
README.md for the pipeline and SPIKE.md for the capture-feasibility findings it depends on.

## Build, test and lint

```bash
make build      # binary into ./take5, version injected via ldflags
make test       # race detector + coverage
make lint       # golangci-lint, full repo
make lint-new   # golangci-lint, only lines changed since main
make prep       # fmt + lint-new + test — run before every commit
go test -run TestName ./internal/...   # single test
```

**Run `make prep` before every commit.**

**`make lint` (full repo) must stay at 0 issues.** The port-era backlog it used to carry
(`wrapcheck`, `revive`) is gone. `make prep` and CI run `--new-from-rev=main` because it's
faster on a working branch, but that's an optimization, not a lower bar — run the full
`make lint` before anything that lands on `main`, and fix what it reports rather than
widening `.golangci.yml`.

## Architecture

- `cmd/take5/main.go` — subcommands (`host`, `install`, `doctor`, `render`,
  `analyze`, `voice`, `version`) dispatched by hand off `os.Args[1]`, stdlib `flag` per
  subcommand. `host` never writes to stdout except through the framed protocol — stdout *is*
  the protocol — so all its logging goes to stderr.
- `internal/host` — the native-messaging host: framing (`framing.go`), the session
  lifecycle (`host.go`), and the wire protocol types (`protocol.go`). `history.go`'s
  `ListSessions` asks `internal/postproduction` for each session's status rather than parsing
  artifacts itself.
- `internal/session` — writes a session's frames/events/network records to disk as they
  arrive and finalizes `session.json`. Also writes `voice.webm` (raw mic capture, opt-in) when
  present — see `VoiceFile`'s doc comment for why it isn't named `voice.wav` despite the rest
  of the pipeline's prose calling it that.
- `internal/director` — turns `session.json` (+ `voice.json`, when present) into
  `project.json`: pause classification, camera keyframes, cursor path, annotations, timeline,
  voice cue placement (`voice.go`). Pure, deterministic, no I/O.
- `internal/render` — compiles `project.json` into `demo.mp4` through two FFmpeg passes: Pass
  A (`temporal_renderer.go`) encodes each `project.Timeline` segment with its own small FFmpeg
  process, then stitches the clips with the concat demuxer (`-c:v copy`) — the same
  component-encode-then-concat shape `internal/plate` uses for `raw.mp4` — rather than one
  `filter_complex` spanning every segment, which stopped scaling once a timeline reached
  dozens of segments; a probe-and-compare guard right after Pass A refuses to hand Pass B a
  video whose actual length doesn't match `project.DurationMs`. Pass B (ASS subtitle overlay,
  filter graph construction, audio mixing including placed voice clips) is unchanged.
- `internal/postproduction` — owns a session directory's artifact lifecycle: canonical
  filenames, freshness/invalidation between `session.json`/`voice.json`/`project.json`/
  `demo.mp4`, the plate → compact → optional voice → analyze → render stage order (`Render`;
  frames are compacted as soon as raw.mp4 probes usable, not after the full render succeeds),
  `Analyze`, and status inspection (`Status`, what `internal/host/history.go` calls). Narration
  credentials/config resolution stays in `cmd/take5` and is passed in as a
  `VoiceStage` closure — this package only decides *when* to attempt it and that its failure is
  non-fatal, not *how* to authenticate.
- `internal/install` — registers the binary as a native-messaging host per browser/OS, and
  `doctor`'s environment checks.
- `internal/transcribe` — thin exec wrapper around whisper.cpp's `whisper-cli` binary (same
  process-shelling pattern as `internal/render`'s FFmpeg wrapper, not a cgo binding).
- `internal/voiceover` — the `voice` pipeline stage: LLM transcript rewrite
  (`Rewriter` interface, ID-keyed batch calls) and TTS synthesis (`TTSProvider` interface,
  default implementation shells out to `edge-tts`), orchestrated into `voice.json` +
  `voice/*.mp3`. The only package that touches the network or calls a paid API.
- `extension/` — the MV3 extension (service worker, content script, and an offscreen document
  used only for opt-in mic capture — `chrome.runtime.connectNative` does not exist inside one,
  confirmed in `SPIKE.md` §1.2, so the native port stays in the service worker and the
  offscreen document relays audio chunks up via `chrome.runtime.sendMessage`); not part of the
  Go build.
- `test/fixtures/corpus/` — golden fixtures: `session.json` in, byte-identical
  `project.json`/filter graphs/ASS out. `tools/gen-fixtures.mjs` (Node) regenerates the
  inputs; golden outputs are the regression baseline and are not regenerated automatically.
- Node (`package.json`) is dev-only tooling — icons, the manual acceptance app, fixture
  generation, SPIKE.md re-verification. The product is the Go module at the repo root.

## Conventions

- Every exported function that can fail returns `error`; the caller decides whether to log
  and continue (as `host` does per-message) or exit non-zero (as `main`'s subcommands do).
- Wrap errors that need context for debugging: `fmt.Errorf("failed to X: %w", err)`.
- Comments describe current intent and the *why*, never the history of a change; no comment
  should reference "the fix" or a past bug once it's fixed.
- Tests are 1:1 ports of the original JS test suite where one exists (see the `// Port of
  test/*.test.js.` comment at the top of such files) — keep that correspondence when editing
  either side. New Go-only behavior gets ordinary table-driven `testing` tests, no
  third-party assertion library.
- `internal/director` and `internal/render` are pure/deterministic on purpose — no
  wall-clock reads, no I/O inside the transform itself — so the golden fixtures stay stable.
- Exec-wrapper packages (`internal/render`'s FFmpeg wrapper, `internal/transcribe`'s
  whisper.cpp wrapper, `internal/voiceover`'s `edge-tts` wrapper) each carry their own small
  `resolveBin`/`extraBinDirs` pair rather than sharing one across packages — this has now
  happened three times, so it's a confirmed convention, not a one-off: a Chrome-spawned
  process gets the OS default `PATH`, not the shell `PATH` a Homebrew/MacPorts install put the
  binary on (`SPIKE.md` §5), and duplicating ~15 lines per package has stayed cheaper than
  introducing a shared internal package for it.
- `internal/plate` (raw.mp4 assembly) and `internal/render` (Pass A) each carry their own
  "encode small segment, then concat-demuxer the results" implementation rather than a shared
  one — same call as `resolveBin` above, made for the same reason: the two differ in real ways
  (plate concatenates JPEG frames into per-batch clips with explicit `duration` directives per
  entry; render concatenates already-encoded, exact-duration clips with none), so a shared
  helper would need to abstract over that difference for two call sites, which has not been
  worth it yet.

## Dependencies

- Prefer the standard library. `golang.org/x/sys/windows/registry` is the one exception,
  needed for Windows native-messaging registration (`internal/install`).
- Keep the Go version identical in `go.mod` and `.github/workflows/ci.yml`.
- Dependabot is notification-only; update in deliberate batches and run `make prep` after.
