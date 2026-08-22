# Plan — porting the native side to Go

Status: implemented 2026-08-18, against the findings in [SPIKE.md](../../SPIKE.md) §1. All
four phases and their gates passed; the extension's transport was additionally migrated from
WebSocket to native messaging (not itself one of the phases below, but required for the
Phase 4 deletion to leave a working product — see the Go host's Options.CallerOrigin and
extension/service-worker.js). `src/` and the WebSocket receiver are gone; `test/spike/` and
`scripts/make-icons.mjs` remain in Node as planned.

The extension stays JavaScript. What moves is everything under `src/` — the local process
Chrome talks to, and everything it does after the recording ends.

## What this changes about the previous plan

Choosing Go **removes two steps** from the migration order agreed before it. Neither the
transport seam in `receiver/` nor a Node implementation of `install`/`doctor` is worth
building: both exist only to carry the Node helper as far as native messaging, and the Node
helper is what is being replaced. The transport migration happens once, in Go.

The concrete win is narrow and worth stating plainly, because it is the only one: a Go binary
has no interpreter, so the launcher shim from SPIKE.md §1.5 disappears and `install` registers
the binary itself. It does **not** solve the FFmpeg half of that finding — that path still has
to be resolved at install time, in any language.

## Inventory

| Component | Lines | Fate |
| --- | --- | --- |
| `receiver/server.js` | 195 | **not ported** — dies with the WebSocket |
| `receiver/protocol.js` | 110 | rewritten for stdio framing; the validation ports as-is |
| `receiver/session-writer.js` | 146 | direct port |
| `director/*` | 775 | direct port; the main parity risk |
| `render/*` | 500 | direct port; output is text, so it is golden-testable |
| `cli.js` + `pipeline.js` | 227 | rewritten as `host` / `install` / `doctor` / `render` / `analyze` / `logs` |
| `lib/png.js` | 57 | **stays in Node** — only `scripts/make-icons.mjs` needs it |
| `extension/` | — | stays JavaScript |
| `test/spike/` | — | **stays in Node** — it drives Chrome; rewriting it buys nothing |

## The load-bearing principle: parity is checked by a machine

The risk is not that Go is hard. It is silent behavioural drift. Those 775 lines of Director
are accumulated tuning — pause thresholds, the typing beat, zoom steps, shot grouping. A wrong
constant or a different rounding rule does not break the build; it produces a slightly
different video, and that gets noticed a month later.

So the Node implementation is **kept as the oracle until the port is finished**. Both run over
the same `session.json` corpus and their `project.json` output is compared automatically. That
turns "did I port the tuning correctly" from a question about reviewer attention into a test.

## Phases

### Phase 0 — freeze the behaviour (before a line of Go)

Build the fixture corpus: 8–10 real `session.json` files covering fast typing, a long pause
inside one field, a network wait, scrolling, a drag, a navigation, and a recording with no
actions at all. For each, store the `project.json`, the filter-graph strings and the ASS output
that the current Node implementation produces.

Add `tools/parity.mjs`, which compares two `project.json` files by parsed value with a float
tolerance — never byte-wise (see the JSON key-order trap below).

Worth doing even if Go is abandoned: `project.json` is currently pinned by nothing, so any edit
to `config.js` changes it silently.

### Phase 1 — the Go host, with post-production still in Node

The first working binary: stdio framing, frame intake, `SessionWriter`, `install`, `doctor`.
Post-production is delegated — the host shells out to `node src/cli.js render <dir>`.

The point is that the system works at every step, rather than spending the port in pieces.

**Gate:** `npm run spike -- dock`, with the host manifest pointing at the Go binary, must
**pass** — the run where a shebang Node host never spawned at all (SPIKE.md §1.5). That is the
whole reason for the port, and the existing harness measures it.

### Phase 2 — the Director

Port `config`, `pause-classifier`, `network-intervals`, `timeline`, `targets`, `cursor`,
`camera`, `annotations`, and their tests (788 lines).

**Gate:** zero differences from the oracle across the whole corpus. Until then the Go `analyze`
is not wired into the pipeline.

### Phase 3 — the renderer

Port `plate`, `ass`, `ffmpeg-expression`, `temporal-renderer`, `visual-renderer` and the FFmpeg
orchestration.

**Gate, in two steps:** first the generated filter graphs and ASS files match the frozen ones
**character for character** — they are what determines the picture, so they are what to compare,
not the video; then one end-to-end run against `test/fixtures/app.html` produces a `demo.mp4`
with the same duration and frame count.

### Phase 4 — delete Node, package

Remove `src/`, keeping `scripts/make-icons.mjs` and `test/spike/`. GoReleaser, cross-compiled
for darwin/arm64, darwin/amd64, windows/amd64, linux/amd64. The bundled-FFmpeg question belongs
here.

## Layout

```text
go.mod                          module …/take5   (Go 1.26)
cmd/take5/main.go       subcommands on stdlib flag — cobra is not worth it for six
internal/host/                  stdio framing, port lifecycle
internal/session/               SessionWriter, frames/, session.json
internal/director/              config, pauses, timeline, cursor, camera, annotations
internal/render/                plate, ass, filtergraph, ffmpeg exec
internal/install/               host manifests per OS and browser, doctor
testdata/                       fixtures shared with the Node oracle
```

Dependencies: the standard library, plus `golang.org/x/sys/windows/registry` for the Windows
install path.

## Traps to write tests for first

Places where Go behaves differently and nothing fails unless the test is written deliberately:

1. **`sort.Slice` is not stable.** `meaningfulActions` sorts by `startMs` then `endMs` and
   relies on `Array.prototype.sort` being stable, which the spec has guaranteed since ES2019.
   Use `sort.SliceStable`, or events sharing a timestamp will reorder between runs and drag the
   cursor waypoints with them.
2. **`Math.round` rounds halves up; `math.Round` rounds away from zero.** They disagree on
   negatives — `-2.5` gives `-2` in JS and `-3` in Go. Times here are non-negative, so this is a
   mine for later rather than a current bug, but it should be pinned by a test.
3. **Division by zero** yields `Infinity` in JS and rides silently into the JSON; in Go
   `json.Marshal` fails on `math.Inf`. That is an improvement — but only if speeds and durations
   are validated on the way in, rather than blowing up at serialisation time.
4. **JSON key order.** Go structs emit a fixed field order; JS objects emit insertion order.
   Hence parity by parsed value, not by bytes.
5. **`toFixed(6)`** in `temporal-renderer` becomes `strconv.FormatFloat(v, 'f', 6, 64)`. The two
   disagree on rounding boundaries, and the string goes straight into the filter graph — only a
   character-exact golden test catches it.
6. **Go map iteration is randomised.** Anywhere the JS relies on object insertion order — the
   camera's shot grouping — has to become a slice.
7. **Frames are dropped, not queued.** `MAX_FRAMES_IN_FLIGHT` deliberately discards a frame when
   the receiver falls behind: every frame carries its own timestamp, so the plate simply holds
   the previous one longer. In Go that is a buffered channel with a non-blocking send and
   `default: drop` — worth writing deliberately, because the natural channel code blocks.

## What this plan does not do

- **FFmpeg stays an external dependency** until the packaging decision is made separately.
- **The extension still needs Load unpacked.** Go changes nothing there.
- **No installer.** `.pkg` / Windows installer is a layer after phase 4.
- **The 106 tests do not "port automatically."** 788 lines of Director tests are ported by hand;
  the remaining 643 (`protocol`, `render`, `plate`) are rewritten against a different transport.

## Estimate

Phase 0 half a day, phase 1 two to three days, phase 2 three days, phase 3 three to four days,
phase 4 one day. Roughly two weeks of focused work, excluding signing and notarisation if a
bundled FFmpeg is added later.
