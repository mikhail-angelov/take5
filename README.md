# take5

Records a real walkthrough of a web app **in your existing, already-authenticated Chrome
tab**, and automatically turns it into a short, smooth, readable product demo.

No Playwright, no browser replay, no video editor. The browser records what really happened; a
local Go binary decides how the viewer should see it. The one exception: an optional,
**off-by-default** voice-narration feature (see [Privacy](#privacy)) that sends transcript
text — never raw audio — to a cloud LLM for cleanup and the LLM's rewritten text on to a TTS
service, only if you turn it on.

```text
raw 12 s recording          demo.mp4 (5.2 s)
─────────────────────       ──────────────────────────
click Generate              smooth cursor → Generate
…8 s of server work…        click + brief loading beat
…5 s of you thinking…       compressed wait, result reveal
click Save                  smooth cursor → Save, zoomed
```

## How it works

```text
Chrome extension (MV3)          Local host (Go binary)
  service worker                  native-messaging port  → frames/ + session.json
    recording lifecycle           Plate assembly          → raw.mp4
    Page.startScreencast          Director                → project.json
    network timing                Renderer                → demo.mp4 (two FFmpeg passes)
  content script
    pointer / click / input
    scroll / keys / targets
```

Capture runs over the DevTools protocol rather than `tabCapture`, because the screencast
reads the compositor output below the layer the pointer is drawn on: your own cursor stays
visible while you record, and never lands in the frames. The extension talks to the host over
`chrome.runtime.connectNative`, which keeps the service worker alive for the length of the
recording without an offscreen document. See [SPIKE.md](SPIKE.md).

The extension captures **what happened**. `session.json` contains no editing decisions. The
Director turns it into `project.json`, a complete deterministic post-production plan, and the
renderer compiles that into FFmpeg filter graphs. Because the plan is a separate artefact,
you can re-render without recording again.

Every rendered demo carries a quiet background-music loop plus synthetic click and typing
effects. The sounds are aligned to the edited timeline, so they remain in sync when waits are
trimmed or accelerated; typing is rate-limited to stay unobtrusive. The bundled CC0 assets and
their sources are listed in [`internal/render/assets/NOTICE.md`](internal/render/assets/NOTICE.md).

## Install

The product is two separate pieces that Chrome's extension model requires to stay separate,
and **both are required** — the extension alone has nothing to record to, and the binary alone
has nothing to drive it:

1. **`take5`**, a local binary Chrome launches for you as a native-messaging host. You
   never run it by hand.
2. **The extension**, loaded unpacked into Chrome, since the project isn't on the Chrome Web
   Store.

Requirements: **FFmpeg** on your `PATH` (`brew install ffmpeg`).

### 1. Get the binary

Either build it from source:

```bash
go build -o take5 ./cmd/take5
```

or download the archive for your OS/arch from the project's GitHub Releases page and extract
`take5` from it — no Go toolchain needed either way.

Then register it — from a clone, `manifest.json` is already at the default path:

```bash
./take5 install
```

If you downloaded the binary instead of building from source, get the extension first (step 2
below) and point `install` at its manifest:

```bash
./take5 install -extension-manifest path/to/take5-extension/manifest.json
```

`install` registers the binary as Chrome's native-messaging host, deriving the extension id
from that manifest's committed `key` field. Run `./take5 doctor` any time to re-check
the binary path, FFmpeg, and the installed manifest.

### 2. Load the extension

Either use the `extension/` directory from a clone of this repo, or download and unzip
`take5-extension.zip` from the same GitHub Release as the binary — either way you end
up with a folder to point Chrome at, with no build step of its own.

Open `chrome://extensions`, enable **Developer mode**, click **Load unpacked**, and select that
folder. The extension's icon should appear in the toolbar; if **Start recording** fails with a
notification that it can't reach the host, re-run `./take5 doctor`.

## Record

1. Open the app you want to demo and log in normally.
2. Navigate to where the demo should begin.
3. Click the extension action to open the popup, then **Start recording**. The badge turns
   **REC**.
4. Perform the walkthrough. Hesitate, wait for slow requests, wander the mouse — that is the
   point.
5. Open the popup again and click **Stop recording**.

Chrome spawns the host itself; there is nothing to run in a terminal. A notification reports
where the demo landed:

```text
~/take5-output/2026-08-17-120102/demo.mp4
```

The popup's **Recording history** button opens a tab listing past sessions and, for each one,
whether the demo finished rendering, is still processing, or failed — read straight from what
each session's directory contains, since the render step runs detached from the extension and
has no way to report back on its own.

The recorder always takes **the tab that was active when you started recording**, so bring
that tab to the front first — switching windows before clicking Start records whatever was in
front there. Recording follows normal navigation within the same tab. Recording a second tab,
or two tabs at once, is out of scope.

## Commands

| Command | What it does |
| --- | --- |
| `take5 install [-key <base64>]` | Registers this binary as Chrome's native-messaging host. |
| `take5 doctor` | Re-checks the binary path, FFmpeg/ffprobe, and every installed host manifest. |
| `take5 render <session-dir>` | Re-renders `demo.mp4` from the recorded frames + `project.json`. Never touches Chrome or your app. |
| `take5 analyze <session-dir>` | Regenerates `project.json` from `session.json` without rendering. |
| `take5 voice <session-dir> [-openrouter-key <key>] [-voice <name>] [-language <code>] [-force]` | Opt-in: transcribes `voice.webm` locally (whisper.cpp), rewrites it via a cloud LLM, synthesizes narration via TTS, writes `voice.json` + `voice/*.mp3`. No-op if `voice.json` already exists, unless `-force`. Requires `whisper-cli`, a ggml model, an OpenRouter API key, and `edge-tts` — see `take5 doctor`. |

Editing a constant in `internal/director/config.go` and re-running `analyze` + `render` is
the supported way to retune the result.

## What ends up on disk

```text
~/take5-output/<session>/
├── frames/         temporary JPEG capture, encoded incrementally during recording; removed once raw.mp4 is confirmed usable
├── segments/       temporary ~20s H.264 segments frames/ gets encoded into as it arrives; removed with frames/
├── session.json    what happened: events + network timing, no editing decisions
├── journal.jsonl   append-only log of every event/network record, for recovering session.json if the process is killed before it can write them itself
├── debug.log
├── raw.mp4         durable re-render source, assembled from segments/ (stream copy, no re-encode) during post-production
├── voice.webm      raw mic capture — only present if voice annotations were enabled
├── voice.json      the `voice` stage's transcript + rewrite + TTS plan (see below)
├── voice/*.mp3     synthesized narration clips voice.json points at
├── project.json    the post-production plan
└── demo.mp4        the demo
```

`frames/` and `segments/` are temporary source material — see
`docs/plans/2026-08-22-segmented-plate-recording.md` for how frames get encoded into segments
during recording and assembled into `raw.mp4` afterward. After a successful render they are
removed automatically, because `raw.mp4` is a complete source for later `render` runs. If
rendering fails — or the process was killed mid-recording — the original frames, segments and
session data are kept so `take5 render` can recover from them (`plate.EnsureRaw`,
`session.Recover`) rather than losing the recording outright.

`voice.webm` is **kept indefinitely**, unlike `frames/` — deliberately the opposite retention
policy. `raw.mp4` is a lossless substitute for `frames/`, so deleting the JPEGs loses nothing;
`voice.json` and its TTS clips are not a lossless substitute for `voice.webm` — the LLM rewrite
and TTS synthesis are one-way, so re-running `take5 voice` with a different model,
prompt, or TTS voice needs the original recording. It's also small: audio capture runs at
roughly 15 KB/s, two to three orders of magnitude under video. `take5 voice` itself is
a no-op if `voice.json` already exists (each run is a real LLM + TTS bill, unlike the free,
always-safe-to-redo `analyze`/`render`) — pass `-force` to redo it. ⚠️ This retention policy is
a product decision, not just an engineering default, and is explicitly flagged as revisable.

## What the Director decides

| Situation | Treatment |
| --- | --- |
| Gap ≤ 800 ms | Kept — short pauses make the result readable. |
| Gap > 1.2 s, no network activity | Middle removed; ~500 ms kept after the action, ~250 ms before the next. |
| Gap overlapping a real request | Compressed, not deleted: the click's aftermath and the result reveal stay at 1×, the middle is sped up to land around 1–1.5 s. |
| Pause while typing in one field | Collapsed to a fixed 250 ms beat before the next keystroke, whatever its real length, so the typing keeps an even pace. Gaps under 600 ms are left exactly as typed. |
| Everything after the last action | Compressed, never truncated: you stop the recording when the demo is done, so the last frame is the closing shot. Cutting at a fixed offset would end on whatever was still rendering. |
| Long-lived connection (> 30 s) | Ignored — a streaming request must not mark the whole video as a wait. |
| Small target clicked | Camera zooms (max 1.4×), nearby targets share one shot. |
| Large obvious target | No zoom; the cursor and the pulse are enough. |
| Small labelled target, or a shortcut | A short caption or key badge is burned in. |

The final cursor is entirely synthetic: it travels between meaningful targets with cubic
easing, so your real mouse wandering never appears.

## Privacy

- The host only ever talks to Chrome over the native-messaging port Chrome itself opens; it
  never listens on a network socket.
- Typed text is **never** recorded — an `input` event stores only its timing and the field's
  label, never its value.
- Printable keystrokes are not recorded; only modifier combinations and named keys.
- No cookies, authorization headers, request bodies or response bodies are captured. Network
  records carry timing and type only.
- The initial page URL is stored with its query string and fragment stripped.
- The host never needs access to your Chrome profile, cookies or credentials.
- **Voice narration is opt-in and off by default** (a checkbox in the popup,
  `chrome.storage.local`, unchecked/unset means off). With it off, capture/`analyze`/`render`
  are byte-identical to having the feature not exist at all. With it on: your microphone is
  recorded to `voice.webm` and transcribed **locally** by whisper.cpp — the raw audio never
  leaves your machine. Only the resulting **text**, already reduced to short transcribed
  segments, is sent to a cloud LLM for rewriting and a cloud TTS service for synthesis (the
  `take5 voice` step). This is the one part of the pipeline that talks to the network
  or a paid API; everything else in this document is unaffected by whether it's on.

## Development

The product is the Go module at the repo root:

```bash
go test ./...                 # director, host protocol, renderer (golden + end-to-end)
go build -o take5 ./cmd/take5
```

`internal/render`'s golden tests check the Director and renderer against the frozen fixtures
in `test/fixtures/corpus/` — session.json in, byte-identical project.json/filter
graphs/ASS out. `tools/gen-fixtures.mjs` (Node) regenerates that corpus's `session.json`
inputs if a new scenario is needed; the golden outputs alongside them are the regression
baseline and are not regenerated automatically.

A few dev-only tools remain in Node — see `package.json`:

```bash
npm run fixture         # serves the manual acceptance app on :8899
npm run icons           # regenerates the extension icons
npm run spike -- all    # re-derives the SPIKE.md findings against a real Chrome (~7 min)
```

`npm run spike` launches its own throwaway Chrome and one of its four experiments
deliberately takes five and a half minutes. See [test/spike/README.md](test/spike/README.md).

`npm run fixture` serves a small app with a text field, a deliberately slow 5-second request,
a result panel, a small icon button and scrolling content — plus a second page, so you can
verify that recording survives navigation. Use it to exercise the whole pipeline by hand.

See [SPIKE.md](SPIKE.md) for the capture-feasibility and native-messaging findings the
synthetic cursor and the transport depend on, and
[docs/plans/go-port.md](docs/plans/go-port.md) for how the Go port was carried out.
