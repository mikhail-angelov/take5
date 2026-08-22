# Voice spike — microphone capture feasibility

The experiments behind Task 1 of
[`docs/plans/2026-08-19-voice-annotations.md`](../../../docs/plans/2026-08-19-voice-annotations.md).
Same discipline as [`test/spike/README.md`](../README.md): a real Chrome, a throwaway
profile, findings that can be re-derived rather than trusted.

```bash
node test/spike/voice/run.mjs
```

Launches Chrome with `--use-fake-device-for-media-stream --use-fake-ui-for-media-stream`
(a synthesized audio input device, prompt auto-accepted), loads a throwaway extension that
opens an offscreen document, captures mic audio with `MediaRecorder`, and relays each chunk
up to the service worker over `chrome.runtime.sendMessage` → `connectNative`, concurrently
with a synthetic 33 ms-cadence message stream standing in for the real video frame path. The
host just logs what arrives, like `test/spike/hosts/host.js` does for Spike 1.

## Findings

### 1. `getUserMedia` is reachable from an offscreen document, with the native port staying in the service worker

**Yes**, and this was never really in doubt for the offscreen-document half — SPIKE.md §1.2
already established that `chrome.runtime.connectNative` **does not exist** inside an
offscreen document (the function itself is `undefined` there, not merely restricted). So the
architecture this spike validates is: the native port lives in the service worker exactly as
it already does for video, and the offscreen document — needed only because the service
worker itself cannot call `getUserMedia` — relays audio chunks up via
`chrome.runtime.sendMessage`. That relay is the thing actually under test here, and it works:

```text
<- {"offscreen":"created"}
<- {"relayed":{...,"event":"getUserMedia resolved","tracks":1}}
<- {"relayed":{...,"event":"recorder-started"}}
```

The native port opened at service-worker startup and never closed until Chrome itself was
killed at the end of the run (`STDIN CLOSED` appears only after, matching the `lifetime`
experiment's own pass criterion in Spike 1).

### 2. Mic-permission UX: mechanically persists per-origin; real first-prompt behavior is NOT proven here

`navigator.permissions.query({ name: "microphone" })` read `"granted"` both before and after
the `getUserMedia` call:

```text
<- {"relayed":{...,"event":"permission-before","state":"granted"}}
<- {"relayed":{...,"event":"getUserMedia resolved","tracks":1}}
<- {"relayed":{...,"event":"permission-after","state":"granted"}}
```

**This does not prove what Task 1 actually asked.** `--use-fake-ui-for-media-stream`
auto-grants the mic for the whole profile — permission reads `"granted"` even *before* the
first `getUserMedia` call in this run, which means the flag that makes this harness
deterministic is exactly the flag that erases the one signal (a real, first-time permission
prompt) the question is about. What this run *does* show: the extension-origin permission
model itself works the same way it does for a normal page (`chrome-extension://<id>/` is the
origin `permissions.query` and the grant are scoped to), so a grant obtained once should
carry across separate recordings without re-prompting, the same way site permissions do for
a website — but that's an inference from the platform's general permission model, not
something this fake-UI harness observed directly.

⚠️ **Needs manual, real-Chrome confirmation** (no fake-UI flags): does the permission prompt
render at all for an offscreen document — which is a hidden, non-tabbed page type — and if
so, does accepting it once genuinely avoid a re-prompt on the next recording? Carried forward
as open work for Task 2 and the manual-verification pass in Post-Completion; not resolved by
this spike.

### 3. Audio relay does not destabilize a concurrent frame-like message stream on the same port

A synthetic 33 ms-cadence stream (120 messages, standing in for `Page.screencastFrame`
traffic) ran on the same `connectNative` port for the whole capture, interleaved with 17 real
audio chunks (`MediaRecorder` at a 250 ms timeslice, ~73 KB total over ~5 s — roughly 14.6
KB/s, two to three orders of magnitude under the 10–15 MB/s video capture rate SPIKE.md
measures):

```text
"syntheticFrameCount": 120,
"syntheticFramesInOrder": true,
"maxFrameGapMs": 38
```

All 120 synthetic frames arrived, strictly in order, with no gap exceeding 38 ms against a
33 ms nominal cadence — i.e. no drops and no meaningful jitter attributable to the audio relay
sharing the port. Audio's bandwidth is low enough relative to video that this is not
surprising, but it was asked for explicitly and is now measured, not assumed.

## What Tasks 2 and 9 need to change as a result

Nothing invalidates the plan's assumptions. Specifically for Task 2:

- Build the offscreen document with `reasons: ["USER_MEDIA"]` (not `"BLOBS"`, which
  Spike 1's now-deleted offscreen experiment used only to test `connectNative` reachability).
- The relay path is `offscreen.js` → `chrome.runtime.sendMessage` → service worker →
  `port.postMessage`, mirroring this spike's `sw.js`/`offscreen.js` almost exactly. No new
  protocol primitive is needed beyond a message shape distinguishing audio chunks from frame/
  event messages on the wire (`internal/host/framing.go`'s existing length-prefixed JSON
  framing is transport-agnostic to payload shape already).
- `MediaRecorder`'s `audio/webm;codecs=opus` output is a container format, not raw PCM —
  Task 3's `whisper.cpp` wrapper needs to either accept webm/opus directly (whisper.cpp itself
  does not decode compressed audio; FFmpeg, already a project dependency, can transcode
  webm/opus → WAV before the ASR step) or the capture side should request a different
  `MediaRecorder` mimeType. Not decided here — flagged for Task 2/3's implementation to
  resolve, since this spike used webm/opus purely because it is Chrome's default and was never
  meant to fix the production format.
- Task 9's toggle gates offscreen-document creation entirely when off (per Task 2's own
  checklist item) — this spike changes nothing about that; it only proves the "on" path works
  mechanically.

⚠️ Per the plan's own instruction: this spike's real-Chrome manual-prompt caveat (finding 2)
should be confirmed by hand before Task 2 is considered done, not just assumed from this
harness's fake-UI run.
