# Spike 0 — Capture Feasibility

Spec §19.1 makes synthetic-cursor work conditional on how the capture backend treats the
native mouse cursor. This document records the decision the implementation is built on and
the checklist to re-verify it on a new machine.

## Questions and answers

### 1. Does `chrome.tabCapture` include the native OS mouse cursor?

**Yes.** (Corrected 2026-08-17; the original spike answered "no".)

Chrome composites the pointer into the tab capture stream. Verified by cropping the raw
capture around a recorded click's coordinates: the macOS arrow is burned into the frame, on
the plate, before any rendering pass runs.

That is the fallback outcome named in §19.1, so the pointer has to be removed at capture
time — see question 2. It cannot be left in: post-production cuts pauses and time-warps the
timeline, so a burned-in pointer would teleport around underneath the synthetic one.

The corollary is the useful part: the composited pointer is the *tab's* pointer, i.e. the
one CSS decides. Chrome does not draw it while the cursor is over an element with
`cursor: none`, and it also drops it while the user types into a text field.

### 2. If it is included, can it be disabled?

Not through a constraint. `chromeMediaSource: "tab"` has no cursor option, and the
[`cursor` constraint](https://developer.mozilla.org/en-US/docs/Web/API/MediaTrackSettings/cursor)
that `getDisplayMedia` once exposed was never implemented by Chrome and is no longer in the
spec — neither `getDisplayMedia({ video: { cursor: "never" } })` nor
`track.applyConstraints({ cursor: "never" })` has any effect in Chromium. Switching capture
backends therefore would not have helped.

CSS works — `cursor: none !important` on the recorded document does keep the pointer out of
the frames — but it is not usable: the hidden pointer and the captured pointer are the same
one, so the operator would have to drive the demo blind. Rejected.

### 2a. Is there a capture backend that omits the pointer?

**Yes: `Page.startScreencast` over the DevTools protocol** (`chrome.debugger`). This is what
Playwright and Puppeteer record through, and it is why their videos never contain a pointer.

Verified locally rather than assumed. A probe page was opened in its own Chrome instance,
raised with `Page.bringToFront`, and the OS pointer parked on a known element. At that
instant, from the same screen region:

- `screencapture -C` (control): the macOS arrow is there;
- the `Page.screencastFrame` payload (test): no pointer anywhere.

The screencast taps the renderer's compositor output, below wherever the pointer is drawn.
The operator keeps their pointer on screen; only the capture is clean. Same reason
`Page.captureScreenshot` never contains a cursor.

Costs: the `debugger` permission and a "Chrome is being debugged" infobar for the length of
the session; DevTools cannot be open on the recorded tab; frames arrive as images with
timestamps, so the helper assembles the plate instead of receiving a WebM.

The sharpest cost, found the hard way: **Chrome vets every frame in the tab before allowing
an attach**, not just the top-level document. A single iframe injected by any *other*
extension — password manager, wallet, corporate agent — makes the whole tab undebuggable,
with an error that names neither the frame nor the extension:

```text
Cannot access a chrome-extension:// URL of different extension
```

`tabCapture` had no such restriction, so this is a real regression in reach: a page is
recordable only in a profile whose other extensions leave it alone. `extensionFramesIn()`
in the service worker enumerates the offending frames through `chrome.webNavigation` and
puts their extension ids in the error, since Chrome will not.

The native-app equivalent, for reference: Screen Studio, Cap and Kap all record through
ScreenCaptureKit with `showsCursor = false` and draw their own pointer afterwards — the same
architecture as this pipeline, one layer lower.

### 3. Does behavior differ across macOS / Windows?

Not expected to. Both the compositing and the screencast path are inside Chrome, above the
platform layer. Verified target for MVP is macOS first, per §29; re-run the checklist below
on Windows before claiming it there.

## Consequences baked into the implementation

- **Capture backend is `chrome.debugger` + `Page.startScreencast`.** No `getDisplayMedia`, so
  no picker dialog and no risk of the user sharing the wrong surface — and, per question 1,
  no pointer in the plate. `tabCapture` is not usable for this pipeline at all.
- **The plate is pointer-free**, so `src/director/cursor.js` synthesizes the entire pointer
  track from meaningful action targets rather than trying to hide a real one.
- **Capture resolution is not assumed.** The screencast delivers the tab viewport in *device*
  pixels, scaled down uniformly to fit the `maxWidth`/`maxHeight` in `SCREENCAST`, so it is
  frequently not `cssViewport × devicePixelRatio`. Events are captured in CSS pixels. The
  renderer therefore probes the real video dimensions with `ffprobe` and scales every
  coordinate by `videoWidth / viewport.width`. Nothing in the Director hardcodes a
  resolution. Note this scaling is only correct because the screencast never letterboxes;
  `tabCapture` did, which silently skewed every coordinate.
- **Frames carry their own timestamps**, so the receiver stores the sequence as-is and
  `src/render/plate.js` assembles the constant-rate plate. A page that paints nothing emits
  no frames and costs nothing; the plate holds the last one.
- **Frame rate is probed, not assumed.** The ASS overlay is sampled at the plate's frame rate
  so cursor motion lands exactly one sample per frame.

## Fallback, if a future Chrome changes this

If `Page.startScreencast` ever starts compositing the pointer too, no in-browser backend is
left: the `cursor` constraint is unimplemented in Chromium (question 2), so `getDisplayMedia`
would burn in the same pointer while additionally forcing a surface picker on every
recording.

Two escape hatches, in order of preference:

1. **Native capture**, the way Screen Studio and Cap do it: a macOS helper on
   ScreenCaptureKit with `showsCursor = false`, cropped to the tab's content rect. Best
   quality, costs a native binary and a screen-recording permission.
2. **Post-production removal.** `session.json` already carries the pointer at 20 Hz, enough
   to mask the cursor rectangle in Pass A. Degrades the plate wherever the mask lands.

## Re-verification checklist

Run this whenever Chrome majors change, or on a new OS:

1. Start the helper: `take5 start`.
2. Open `test/fixtures/app.html` in a tab and click the extension action.
3. Move the mouse continuously, click **Generate**, navigate to the second page, click the
   extension action again.
4. Inspect the raw capture, not the demo:
   - open a handful of `demo-output/<id>/frames/*.jpg` taken while the mouse was moving and
     confirm **no pointer is visible anywhere**;
   - confirm `frames.json` timestamps span the whole recording, across the navigation (§6.4);
   - `ffprobe -v error -show_entries stream=width,height,r_frame_rate -of default=nw=1
     demo-output/<id>/raw.mp4` — the frame should be the viewport in device pixels, with no
     black bars on any side.
5. Confirm `session.json` contains events with `t` values spanning the whole recording,
   including after the navigation.

Exit criterion: a full-session frame sequence with no native cursor in it, and a plate whose
duration matches `session.json`'s `durationMs`.

# Spike 1 — Native messaging as the transport

Measured on 2026-08-17, macOS 15 (Darwin 25.5.0), Chrome 151.0.7922.138, Node 25.6.1. Every
number below comes from a run against a real Chrome with an isolated `--user-data-dir`, not
from the documentation.

## Method

Three extensions, one pinned key so all three load under the same id, one run each in a fresh
profile, and then nothing is touched for the length of the run.

Two independent instruments, because the obvious one is invalid:

- **The host's own log.** Chrome closes the host's stdin the instant it tears the port down,
  so a passive host that timestamps its own spawn, its messages and its EOF measures the
  port's lifetime without observing the browser at all.
- **A CDP observer on the *browser* target** using `Target.setDiscoverTargets`, which reports
  service workers appearing and disappearing. It never calls `Target.attachToTarget`:
  **attaching to a service worker keeps it alive** — that is what DevTools does — and would
  have voided the whole experiment.

## Questions and answers

### 1. Does an open native-messaging port keep the MV3 service worker alive?

**Yes, indefinitely, through total silence.**

Extension A opens one port at service-worker top level, sends one message, and then nothing
happens for 5.5 minutes. The worker was never evicted, the host process was never respawned
(one pid throughout), and stdin closed only when Chrome was killed:

```text
[+   1.2s] service worker STARTED  chrome-extension://…/sw.js
[+  31.3s] service worker EVICTED  chrome-extension://fignfif…/service_worker.js   ← unrelated
[+  33.8s] service worker EVICTED  chrome-extension://ghbmnnj…/service_worker.js   ← unrelated
[+ 331.2s] before killing chrome: host port STILL OPEN
[+ 329.9s] pid=97877 STDIN CLOSED — chrome tore the port down                      ← the kill
```

Two unrelated extensions in the same browser were evicted on the normal idle timer at ~31 s,
which is the first control: eviction was working normally in that profile.

The second control is extension C — the same extension, same profile, same CDP connection,
with the port removed. Its worker was evicted at **31.1 s**, in the same second as the
unrelated ones. So the port is the cause, and neither the debugging connection nor
`Extensions.loadUnpacked` distorts the result.

The historical five-minute hard cap on service-worker lifetime does not apply: 330 s is past
it, and 30 s of silence — the interval that matters here, since a page that paints nothing
sends no frames — is not close.

### 2. Is `chrome.runtime.connectNative` available in an offscreen document?

**No.** It is not merely restricted; the function does not exist:

```text
<- {"relayed":{"from":"offscreen","typeofConnectNative":"undefined"}}
<- {"relayed":{"from":"offscreen","event":"connectNative threw",
     "error":"TypeError: chrome.runtime.connectNative is not a function"}}
```

This would have been fatal for the WebSocket architecture, where the socket has to live in an
offscreen document to survive worker eviction (spec 6.3). Given question 1 it costs nothing:
the port belongs in the service worker, and **the offscreen document disappears entirely** —
`offscreen.html`, `offscreen.js`, the `offscreen` permission, the frame relay and its send
chain, roughly 150 lines and one class of races.

### 3. Is the extension id reproducible from a committed key?

**Yes.** A `key` in the extension manifest pins it. The id derived offline — sha256 of the DER
SPKI, first 16 bytes, hex digits mapped onto `a–p` — was `hmppeamjpaijbcebhomfonkodfjnfobb`,
and Chrome loaded the extension under exactly that id.

So `install` can compute the id itself and write a host manifest whose `allowed_origins` is a
single exact origin. It never has to ask the user for an id, and the manifest is the same on
every machine.

Chrome also passes the calling origin to the host as `argv[1]`
(`chrome-extension://<id>/`), so the host can verify its caller independently of the manifest.

### 4. Is the base64 envelope slower than the binary WebSocket it replaces?

**No — it is about seven times faster**, because the transport was never the cost.

Extension D pushes 300 identical payloads of 300 KB (a 2560×1600 q85 screencast frame) down
both paths, back to back, in one browser. Both receivers count *decoded* bytes.

```text
native:    300 frames, 90.0 MB in  142 ms = 634 MB/s
websocket: 300 frames, 90.0 MB in  990 ms =  91 MB/s
sender-side cost inside the service worker: native 173 ms, websocket-via-offscreen 1007 ms
```

The 33 % the base64 envelope adds on the wire is real and irrelevant: a local pipe is not the
bottleneck. What the WebSocket path pays for instead is the architecture forced on it — a
`chrome.runtime.sendMessage` hop of the whole payload into the offscreen document, and a
`fetch("data:…")` round trip per frame to undo the base64 Chrome had already produced. The
native path does neither: the string arrives from `Page.screencastFrame` and leaves through
`port.postMessage` untouched, and the only decode happens in Node, where the JPEG has to be
turned into bytes anyway to be written to disk.

Per frame, sender-side: **0.58 ms native against 3.36 ms**. At 30 fps that is 1.7 % of a core
instead of 10 %, in the service worker, on the path that drops frames when it falls behind
(`MAX_FRAMES_IN_FLIGHT`).

Caveat on the number: this is a burst, not a sustained recording, and it excludes writing the
JPEGs to disk. Real capture runs at roughly 10–15 MB/s, which both transports clear easily.
The conclusion is not "native is fast enough" — both are — it is that the service-worker cost
per frame falls by ~6×.

### 5. What environment does Chrome give the host?

**`PATH=/usr/bin:/bin:/usr/sbin:/sbin`, and that breaks a `#!/usr/bin/env node` host outright.**

This is the one finding a spike run from a terminal will hide, and it hid it twice here before
being caught:

| How Chrome was started | PATH the host received | Host spawned? |
| --- | --- | --- |
| from a shell (`spawn`) | the full shell PATH | yes — **false positive** |
| `open -na` from a shell | the full shell PATH, forwarded by `open` | yes — **false positive** |
| with the GUI environment (`env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin`) | `/usr/bin:/bin:/usr/sbin:/sbin` | **no** |

`launchctl getenv PATH` is unset on this machine, so an app started from the Dock gets the
default system PATH. Node here is nvm's, at `~/.nvm/versions/node/*/bin/node`, which is not in
it — so `/usr/bin/env node` finds nothing, the host never starts, and the extension sees only
a port that failed to open. Nothing is logged anywhere.

Registering a launcher with the interpreter's absolute path baked in fixes it completely:

```sh
#!/bin/sh
exec "/Users/…/.nvm/versions/node/v25.6.1/bin/node" "/…/host.js" "$@"
```

Verified: with the same GUI environment, the host spawns and reports
`PATH=/usr/bin:/bin:/usr/sbin:/sbin`.

**The same trap catches FFmpeg.** `ffmpeg` lives in `/opt/homebrew/bin` here, which is equally
absent from that PATH, so the host cannot find it either — and no choice of host language
changes that. Both absolute paths have to be resolved at install time, when a shell's
environment is still available, and recorded. `install` knows its own interpreter as
`process.execPath`; `doctor` re-checks both and is the reason this failure is diagnosable at
all.

## Consequences

- **The transport is `chrome.runtime.connectNative`.** The WebSocket receiver, the fixed port
  47823, `isAllowedOrigin` (which today accepts *any* `chrome-extension://` origin, i.e. any
  extension in the profile can feed the helper) and the `EADDRINUSE` advice all go away.
- **The offscreen document goes away**, per question 2 plus question 1.
- **`install` computes the extension id from the committed key**, per question 3, and writes
  one host manifest per detected browser.
- **`install` resolves absolute paths for the interpreter and for FFmpeg and bakes them into a
  generated launcher**, per question 5. Nothing the host runs may be looked up on `PATH`,
  because the host's `PATH` is the system default, not the user's.
- **The host must never write to stdout except framed messages.** stdout *is* the protocol;
  one stray `console.log` corrupts the frame stream silently. The entry point redirects
  `console.*` to stderr before loading anything else.
- **Post-production is detached from the port.** Chrome kills the host when the port closes,
  so `frames/` and `session.json` are written during the session and the render is spawned
  `detached`, leaving `take5 render <dir>` as the recovery path it already is.
- Host→extension messages stay under Chrome's 1 MiB cap: only status, progress and the final
  path travel that way. Extension→host has a 64 MiB cap, two orders of magnitude above a frame.

## Re-verification checklist

Run this whenever Chrome majors change, or on a new OS:

1. `npx take5 install`, then confirm `doctor` reports the manifest, the derived id and
   an executable host path.
2. Load the extension, open a page that paints nothing, and leave it alone for 3 minutes.
3. Confirm the host log shows one pid, a continuous heartbeat, and no `STDIN CLOSED` until the
   browser is closed.
4. Record a 90-second walkthrough and confirm the frame count in `frames.json` matches the
   number of `Page.screencastFrame` events the service worker saw — i.e. nothing was dropped.

Exit criterion: a service worker that survives three minutes of silence with the port open,
and a 90-second recording with no dropped frames.

Note for a future Chrome: `--load-extension` is **ignored** as of Chrome 151, silently and
without a log line, and `--disable-features=DisableLoadExtensionCommandLineSwitch` no longer
revives it. Automated runs must load extensions over CDP with `Extensions.loadUnpacked`.
