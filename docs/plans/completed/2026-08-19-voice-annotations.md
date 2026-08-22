# Voice annotations for the demo screencast

## Overview

Let the user narrate a walkthrough by voice while recording, and turn that narration into
a clean, correctly-paced voiceover synced to the specific action it explains — instead of
requiring the user to speak cleanly and on-beat themselves. The user recording the demo is
not a professional narrator: they stumble, mumble, and mis-time what they say relative to
what they're doing on screen. The feature exists to absorb that, not to require better
delivery from the user.

Pipeline: record raw microphone audio during the walkthrough → transcribe locally
(whisper.cpp) → clean the wording up with an LLM → synthesize clear speech (TTS) → place
each resulting clip on the demo's edited timeline, next to the action it explains, with the
video re-paced around it exactly like waits and typing are re-paced today.

The feature is **opt-in and off by default**. With it off, or with no recorded voice, the
whole pipeline (recording, `analyze`, `render`) behaves byte-identically to how it behaves
today — this is a hard requirement, not an aspiration, and is what keeps the existing golden
fixtures in `test/fixtures/corpus/` valid without modification.

A `docs/research-audio-assets.md`-style spike already validated the core hypothesis by hand:
a real 60s take of rambling Russian narration about a 3D-modeling app was pushed through
whisper.cpp (`ggml-small`, auto-detected `ru`) → an LLM rewrite (via OpenRouter,
`deepseek/deepseek-chat`) → TTS (`edge-tts`, `ru-RU-DmitryNeural`), and the user confirmed the
result "sounds normal" (minor stress-accent issues, not a blocker). A from-scratch scheduling
prototype (built directly against `internal/director`'s real `planSegments`/`buildTimeline`,
then deleted — see "MVP findings" below) additionally confirmed two non-obvious things this
plan is designed around: TTS narration comes out **shorter** than the raw speech it replaces,
and a naive per-cue-independent timing calculation **does** violate the ordering constraint
described below on realistic numbers. Those findings are not hypothetical — they shaped
Tasks 5–6 directly.

## Context (from discovery)

- **Capture side (extension, MV3):** `extension/` — service worker owns the recording
  lifecycle over `chrome.runtime.connectNative`; a content script captures pointer/click/
  input/scroll/keys/targets. There is currently **no audio capture of any kind**. The
  screencast is captured via the DevTools protocol specifically so the user's own OS cursor
  never appears in frames (see `SPIKE.md`); mic capture has no such constraint and is a
  separate, unexplored capability (`getUserMedia`), most likely requiring an offscreen
  document — which the current design deliberately avoids for video. This is genuinely new
  ground for the extension and is scoped as its own spike (Task 1).
- **Host / session (`internal/host`, `internal/session`):** writes `session.json` (events +
  network timing only, "no editing decisions" is a stated invariant in README.md). A raw
  `voice.wav` sits alongside it, keyed to the same source-time origin, the same way
  `frames/` does for video.
- **Director (`internal/director`):** `Direct()` in `director.go` is the single pure
  entry point — session in, `project.json` out, no I/O, no wall-clock reads (this is a
  stated invariant in CLAUDE.md, load-bearing for the golden fixtures in
  `test/fixtures/corpus/`). Relevant existing pieces:
  - `timeline.go` — `buildTimeline`, `sourceTimeToOutputTime`, `RawSegment`/`Segment`. All
    time-mapping must go through here; nothing may re-derive this arithmetic.
  - `pause_classifier.go` — `planSegments`, `classifyGap`, `classifyTypingGap`,
    `classifyHesitation`, `isTypingGap`, `meaningfulActions`. This is where voice-aware
    overrides plug in, conditionally, only for actions that have a paired cue.
  - `audio.go` (uncommitted, in progress alongside this plan) — `planAudio`, `AudioCue`:
    the existing precedent for placing a cue at an output-time instant. Voice cues extend
    this idea to cues with real, non-fixed duration.
  - `config.go` — `Config`, `AudioConfig`, `PauseConfig`, `TypingConfig`: the existing
    per-concern config-struct pattern a new `VoiceConfig` follows.
- **Renderer (`internal/render`):** `audio_renderer.go` (`BuildAudioFilterGraph`) mixes
  fixed-duration synthetic click/typing assets by `tMs`; `assets.go` embeds them via
  `go:embed`. Voice clips are a variable-duration, per-session (not embedded) extension of
  the same mixing idea.
- **CLI (`cmd/take5/main.go`):** subcommands `host`, `install`, `doctor`, `render`,
  `analyze`, `version`, dispatched by hand off `os.Args[1]`. A new `voice` subcommand joins
  this list.
- **`doctor` (`internal/install`):** currently checks the binary path, FFmpeg/ffprobe, and
  installed native-messaging manifests. Needs a new check for whisper.cpp + model presence,
  matching how it already checks FFmpeg.

## MVP findings (from the pre-plan spike — binding constraints on the design below)

- **ASR+LLM+TTS quality is acceptable.** Confirmed by the user listening to real output.
  Not further re-litigated by this plan.
- **The LLM cannot be trusted to preserve array length across a batch rewrite.** A 6-segment
  batch came back as 5 — the model silently dropped a trailing, unfinished segment. In this
  instance the drop was actually correct (the segment was filler), but the prototype
  discovered this by *positional* array matching, which is unsound: a dropped element
  anywhere in the middle would have silently misaligned every cue after it. **Binding
  requirement:** every cue sent for rewrite carries a stable ID; the LLM must return, per ID,
  either rewritten text or an explicit "drop — incomplete/filler" marker. Positional
  (index-based) matching between input and output arrays is not acceptable.
- **TTS narration comes out shorter than the raw speech it replaces** — 1.5–2.5× shorter in
  the spike (deltas from -1.3s to -8.3s on 5-13s segments), because disfluencies and dead air
  get edited out by the LLM rewrite. **Binding requirement:** the segment-timing integration
  must be designed around *compressing* video to a short narration as the common case, not
  around protecting/stretching a segment for a long one.
- **Naive, per-cue-independent lead-in calculation violates the ordering constraint on
  realistic data.** In the prototype, cue 2's natural audio start landed 860ms *before* the
  typing run that cue 1 was narrating had actually finished — because real speech naturally
  runs ahead of the action it's about ("I'll click Generate next" said while still typing).
  **Binding requirement:** lead-in/placement cannot be computed per-cue in isolation; it needs
  a single sequential pass over cues+actions in timeline order (mirroring how `buildTimeline`
  itself is sequential), clamping `audioStart = max(natural_position, prevPairedActionOutputEnd)`.
- FFmpeg in this project is already built with `--enable-librubberband` — pitch-preserving
  audio time-stretch is available if a future tuning pass needs it. Not required for this
  plan; noted as a lever, not used.

## Design decisions locked in (from the brainstorm session — not reopened by this plan)

- **ASR runs locally** (whisper.cpp, invoked as an external binary via `exec` — the same
  pattern FFmpeg already uses in this codebase, not a cgo binding). This is a deliberate
  privacy boundary: the user's raw voice never leaves the machine.
- **Transcript cleanup runs through a cloud LLM**, not manual editing and not mechanical
  filler-stripping. Only the *text* — already reduced to short, imperfect sentences —
  crosses the network, never the raw audio.
- **TTS runs through a cloud API.** The spike stack (OpenRouter/`deepseek-chat` for rewrite,
  free `edge-tts` for synthesis) is good enough to validate the pipeline; the production
  provider choice is an explicit open question (see below), not decided by this plan.
- **Recording is continuous**, like the video — not push-to-talk per step. The user just
  talks while doing the walkthrough, the same as they do everything else in this product.
- **Cue↔action pairing is strictly 1:1, and a cue always explains the action that follows
  it in time** (never a preceding one, never a batch of several). Not every action has a
  paired cue — most clicks in a real demo will remain silent, and for those, today's
  `classifyGap`/`classifyTypingGap` behavior is completely unchanged. **For typing, "the
  action" a cue pairs to is the whole contiguous same-field keystroke run**, treated as one
  unit — matching how `isTypingGap`/`classifyTypingGap` already treat it as one logical span
  today, not each individual keystroke as its own action. A cue never pairs to a single
  keystroke inside a run it doesn't otherwise cover.
- **Pipeline placement:** a new, explicit `voice` stage sits between recording and
  `analyze`, producing a cacheable `voice.json` that `Direct()` reads as one more pure input
  (alongside `session.Events`/`session.Network`). This is the only stage in the whole
  pipeline that touches the network or calls an external ASR process — `analyze` and
  `render` remain exactly as deterministic and offline as they are today, which is why the
  golden-fixture invariant in CLAUDE.md survives this feature.

  ```text
  host    → session.json + voice.wav   (voice.wav is new: raw mic, source-time aligned)
  voice   → voice.json                  (NEW: whisper.cpp -> LLM rewrite -> TTS)
  analyze → project.json                (Direct() reads voice.json as a pure input)
  render  → demo.mp4                    (renderer mixes TTS clips by output time)
  ```

## Open questions (explicitly not decided by this plan — flag, don't guess)

- **Mic capture mechanism in the extension.** `getUserMedia`, almost certainly via an
  offscreen document (supported since Chrome 116) since the service worker itself can't get
  a microphone stream and the current design deliberately has no offscreen document for
  video. This needs the same kind of feasibility pass `SPIKE.md` did for screen capture
  before the approach can be trusted. Task 1 below is that pass — its own findings may
  invalidate specifics later in this plan, and the plan should be revised if so.
- **`voice.wav` retention policy.** `frames/` is deleted after a successful render because
  `raw.mp4` is a complete substitute. Whether `voice.wav` can be deleted once `voice.json` +
  its clips exist, and how a failed/interrupted `voice` run is retried without re-recording,
  is not decided here — Task 8 makes a call but treat it as revisable.
- **Production TTS provider.** Stay on free `edge-tts`, or move to a paid engine (e.g.
  ElevenLabs) for better prosody/stress-accent quality and stability. Not decided; the
  `voice` stage should not hard-code a single provider in a way that makes this an expensive
  later change (see Task 4).
- **Failure handling for the `voice` stage specifically.** It is the one stage with real
  network dependencies (LLM, TTS) and an external process (whisper.cpp) that can be missing,
  slow, or fail outright. What "recording succeeded, voice processing failed" should look
  like to the user is a product decision, not just an engineering one — Task 7 proposes the
  simplest reasonable default (render without narration, surface the failure) but this
  should be confirmed, not assumed final.
- **Multi-language handling beyond auto-detect.** whisper.cpp auto-detects source language;
  whether the LLM rewrite prompt and TTS voice selection should be explicitly driven off that
  detected language (rather than a fixed default) is not decided.

## Development Approach

- **Testing approach:** regular (implementation first, tests alongside/after each task) —
  matches this project's existing convention (see CLAUDE.md: new Go-only behavior gets
  ordinary table-driven `testing` tests; ported behavior mirrors its JS test).
- Complete each task fully, with its tests passing, before starting the next.
- `internal/director` and `internal/render` changes must never change output when no voice
  data is present — verify this explicitly (not just "seems unaffected") wherever it's
  plausible for a change to leak into the no-voice path.
- Run `make prep` before considering any task's code changes done.

## Testing Strategy

- **Unit tests** for every task, Go stdlib `testing`, table-driven where the existing files
  in `internal/director`/`internal/render` already use that style.
- **Golden-fixture regression:** after Tasks 5–6 (Director/render integration), explicitly
  re-run the existing `test/fixtures/corpus/` golden tests and confirm they are still
  byte-identical with no voice data present. This proves the "opt-in, off by default,
  unchanged when off" requirement for everything `internal/render/golden_test.go` actually
  compares — the temporal/visual filter graphs and ASS overlay — which does **not** include
  `BuildAudioFilterGraph`; the corpus suite has never exercised audio mixing, before or after
  this feature. The audio side of the same requirement is proven separately by Task 6's own
  byte-identity test, not by this suite.
- **No e2e/UI test framework exists in this project** (extension is loaded unpacked, manually
  exercised via `npm run fixture`); manual verification against the fixture app is listed
  under Post-Completion instead of an automated e2e task.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, and doc updates achievable inside
  this repository.
- **Post-Completion** (no checkboxes): manual browser testing, API-key/provider setup outside
  the repo, and anything gated on answering the open questions above.

## Implementation Steps

### Task 1: Spike — microphone capture feasibility in the extension

**Files:**
- Create: `test/spike/voice/README.md` (findings, mirrors `test/spike/README.md`'s role for
  screen capture)
- Create: throwaway spike code under `test/spike/voice/` as needed (not shipped)

- [x] determine whether `getUserMedia` audio capture is reachable from an offscreen document
      while a native-messaging port to the host stays open for the recording's duration —
      **yes**; port stays in the service worker (offscreen `connectNative` doesn't exist at
      all, per SPIKE.md §1.2), offscreen document relays audio chunks via `sendMessage`. See
      `test/spike/voice/README.md` finding 1.
- [x] confirm mic-permission UX: when Chrome prompts, whether it can be requested once at
      install/first-use rather than interrupting every recording — **inconclusive by
      automation**; fake-UI flags needed for a headless spike also erase the one signal this
      question needs. ⚠️ Needs manual, real-Chrome confirmation before Task 2 is done — see
      `test/spike/voice/README.md` finding 2 and Post-Completion.
- [x] confirm audio chunks can be streamed to the host over the existing native-messaging
      channel (or determine what format change is needed) without destabilizing the video
      frame stream — **confirmed**: 120/120 synthetic frames in order, max gap 38ms against a
      33ms cadence, with real audio relay running concurrently. See finding 3. Format note:
      `MediaRecorder` output is webm/opus, not raw PCM — Task 3's whisper.cpp step needs an
      FFmpeg transcode or a different capture mimeType (flagged, not decided).
- [x] write up findings in `test/spike/voice/README.md`: what works, what doesn't, and
      which specifics in Tasks 2 and 9 need to change as a result — done.
- [x] flag ⚠️ in this plan and get explicit confirmation before proceeding to Task 2 if the
      findings contradict this plan's assumptions — **no contradiction found**; proceeding to
      Task 2. The one caveat (permission-prompt UX) doesn't invalidate the design, it's a
      manual-verification item the plan already anticipated in Post-Completion.

### Task 2: `voice.wav` capture end-to-end (extension → host → disk)

**Files:**
- Modify: `extension/` (service worker + new offscreen document, per Task 1's findings)
- Modify: `internal/host/host.go`, `internal/host/protocol.go`
- Modify: `internal/session` (writer)

- [x] gate mic capture behind a simple feature flag (`chrome.storage.local["voiceEnabled"]`,
      checked by `isVoiceEnabled()` in `service-worker.js`, default `false` — unset reads
      false); with it off, behavior is identical to today. Task 9 still owns the popup
      checkbox that writes this key.
- [x] add offscreen document + `getUserMedia` capture per Task 1's findings, wired into the
      existing recording lifecycle in the service worker (`extension/offscreen.html`,
      `extension/offscreen.js`, `startVoiceCapture`/`stopVoiceCapture` in
      `service-worker.js`). Capture is kicked off concurrently with `attachDebugger`/
      `readViewport` rather than awaited serially, so the opt-in path adds no visible startup
      delay when it succeeds.
- [x] extend the native-messaging protocol to carry audio chunks alongside existing frame/
      event messages — new `"audio-chunk"` message type (`{tMs, data}`, same shape as
      `"frame"`), validated by `validateAudioChunk` in `internal/host/protocol.go` (factored
      out of `validateFrame` via a shared `decodeTimedPayload`). `session-start` gained an
      optional `audio: {mimeType}` field.
- [x] write the raw mic capture to the session directory from `internal/session`,
      source-time aligned with `session.json` the same way `frames/` is (`Writer.AppendAudio`,
      opened lazily so the file never exists when the toggle is off). ⚠️ **Deviation from this
      plan's naming**: the file is `voice.webm`, not `voice.wav`. Browsers' `MediaRecorder` has
      no WAV/PCM output mode, only encoded containers (confirmed in
      `test/spike/voice/README.md` finding 3) — writing anything named `.wav` here would be a
      lie, and `internal/session`'s own stated invariant is "no post-production decisions, no
      FFmpeg" (package doc comment), so a real WAV transcode has to happen in the `voice`
      stage (Task 3), not here. `voice.wav` throughout the rest of this plan should be read as
      "the artifact Task 3 produces from `voice.webm`", not this file.
- [x] write tests for the host-side protocol changes (`internal/host`) covering both audio
      present and absent — `TestRunWritesVoiceFileWhenAudioChunksArrive`,
      `TestRunLeavesNoVoiceFileWhenAudioAbsent`, `TestRunRejectsAudioChunkBeforeSessionStart`
      in `internal/host/host_test.go`.
- [x] run tests — `make prep` passes, full `go test ./...` passes including the
      `internal/render` golden-fixture corpus (untouched by this task, confirmed still green).

  ⚠️ **Known limitation, not fixed in this task**: audio capture starts as soon as the
  offscreen document is ready, which can be a few hundred ms before the debugger/viewport
  handshake finishes and `session.startedAtEpochMs` is stamped. In the unlikely case the first
  `MediaRecorder` chunk (fires ~250ms after `recorder.start()`) arrives before that handshake
  completes, it is silently dropped — bounded to at most the first quarter-second of
  narration, never a correctness/alignment problem for what does arrive. Left as-is: fixing it
  would mean buffering pre-session chunks in the service worker, which is more machinery than
  this bounded, cosmetic gap justifies. Flagging here rather than silently accepting it.

### Task 3: `whisper.cpp` integration + `doctor` check

**Files:**
- Create: `internal/transcribe/whisper.go` (or similar — exec wrapper, package name TBD at
  implementation time to match project conventions)
- Create: `internal/transcribe/whisper_test.go`
- Modify: `internal/install` (doctor checks)

- [x] implement a thin exec wrapper around the `whisper-cli` binary: given a WAV path,
      return segment-level transcript with timestamps (`internal/transcribe/whisper.go`,
      `Transcribe`/`Result`/`Segment`) — mirrors `internal/render/ffmpeg.go`'s `resolveBin`/
      error-wrapping conventions (duplicated rather than shared across packages, matching this
      codebase's existing precedent of one small resolveBin per exec-wrapping package).
      Segment granularity, not word-level: matches the plan's own `voice.json` schema.
- [x] add a `doctor` check for whisper.cpp binary presence and model file presence
      (`Report.WhisperFound`/`WhisperModelFound` in `internal/install/install.go`, printed by
      `cmdDoctor` in `main.go`) — both explicitly marked "only needed for voice annotations"
      in the doctor output, since unlike FFmpeg they are not a base-product requirement.
- [x] write tests for the wrapper's argument construction and output parsing — `buildArgs`/
      `parseWhisperJSON` split out as pure functions specifically so this could be tested
      against a fixture transcript (`internal/transcribe/whisper_test.go`), no real model
      invocation.
- [x] write tests for the `doctor` check (binary present / absent / model missing) —
      `internal/install/install_test.go`, using `WHISPER_CLI_PATH`/`WHISPER_MODEL_PATH`
      explicit overrides so "absent" is deterministic regardless of whether the dev machine
      happens to have whisper-cpp installed via Homebrew.
- [x] run tests — `make prep` passes (fmt + lint-new + full test suite).

  Note: `ModelPath()` resolves any `ggml-*.bin` found in a short list of known Homebrew/
  MacPorts share directories, or `WHISPER_MODEL_PATH` if set — it does not pin a specific
  model size (small/base/...), matching the plan's own "operator choice" framing rather than
  hard-coding the spike's `ggml-small`.

### Task 4: LLM rewrite + TTS synthesis, producing `voice.json`

**Files:**
- Create: `internal/voiceover/rewrite.go` (LLM call, ID-based cue rewriting)
- Create: `internal/voiceover/tts.go` (TTS call, provider abstracted per open question above)
- Create: `internal/voiceover/voice.go` (orchestration: transcript → rewrite → tts →
  `voice.json` + `voice/*.mp3`)
- Create: matching `_test.go` files for each

- [x] define the `voice.json` cue schema with a **stable ID per cue** — `VoiceCue` in
      `internal/voiceover/voice.go`: id, sourceStartMs, sourceEndMs, originalText,
      rewrittenText (`*string`, nil marshals to JSON `null` = drop), audioFile, ttsDurationMs.
      A dropped cue stays in the array with `rewrittenText: null` rather than being omitted —
      Task 5 (director) is what skips it when building `project.json`.
- [x] implement the rewrite call: `internal/voiceover/rewrite.go`'s `Rewriter` interface +
      `ValidateRewriteResponse` (send all cues in one batch keyed by ID; **fails loudly**,
      not silently truncating, if any ID is missing) plus `openai_rewriter.go`'s concrete
      `OpenAICompatRewriter` (works against OpenRouter or OpenAI — matches the spike stack
      without pinning the production provider, per the open question).
- [x] implement TTS synthesis behind a small interface — `TTSProvider` in
      `internal/voiceover/tts.go`, concrete `NewEdgeTTSProvider()` shells out to the `edge-tts`
      CLI (spike-validated default), matching this project's exec-wrapper convention.
- [x] implement the `voice` CLI subcommand: `take5 voice <session-dir>` in
      `cmd/take5/main.go` (`cmdVoice`) — reads `voice.webm` (per Task 2's naming
      deviation), transcodes to WAV, transcribes, rewrites, synthesizes, writes `voice.json` +
      `voice/*.mp3`. Flags for OpenRouter key/model, TTS voice, and language; fails fast with
      a clear message if the API key, whisper.cpp model, or ffmpeg is missing.
- [x] write tests for ID-preservation and explicit-drop handling, including the exact
      adversarial case the MVP hit (`internal/voiceover/rewrite_test.go`:
      `TestValidateRewriteResponseFailsWhenAnIDIsSilentlyMissing`,
      `...FailsWhenAMiddleIDIsMissing`) and the orchestration-level version
      (`voice_test.go`: `TestSynthesizeCuesFailsLoudlyWhenRewriteDropsAnIDSilently` — confirms
      no TTS calls happen and no `voice.json` is written once validation fails).
- [x] write tests for the TTS provider interface with a fake/stub provider —
      `voice_test.go`'s `fakeTTS`/`fakeRewriter`, exercising `synthesizeCues` end-to-end
      (kept cues get an mp3 + duration, dropped cues get neither, `voice.json` has all 3).
- [x] run tests — `make prep` passes.

  Design note not in the original checklist: `synthesizeCues` (the rewrite+TTS half) is split
  from `Run` (which also transcodes voice.webm→WAV and calls whisper.cpp) specifically so
  tests exercise real orchestration logic without needing whisper.cpp, edge-tts, or network
  access installed — only `ProbeDurationMs` needed a similar seam (defaults to a real
  ffprobe-backed prober, overridable in tests) since duration is measured off the actual
  written file rather than trusted from provider metadata.

### Task 5: `Director` — voice cue pairing and the sequential placement pass

**Files:**
- Create: `internal/director/voice.go` (`planVoice`, cue↔action pairing, sequential
  lead-in/placement clamp)
- Create: `internal/director/voice_test.go`
- Modify: `internal/director/config.go` (new `VoiceConfig`)
- Modify: `internal/director/director.go` (wire `planVoice` into `Direct()`, extend
  `Project`)
- Modify: `internal/director/types.go` if `Session` needs a voice-cues field

- [x] pair each voice cue to the next action after it in source time (1:1) —
      `pairVoiceCues`/`computePairableUnits` in `internal/director/voice.go`, a two-pointer
      walk over time-ordered kept cues and "pairable units". Actions without a paired cue are
      completely untouched (no override ever reaches `planSegments` for them).
- [x] implement the sequential placement pass exactly per the Technical Details pseudocode —
      `planVoicePlacement`: `naturalHold` folded into `actionOutputStart` before the barrier
      clamp, never computed from the cue's raw source start directly. Verified against the
      MVP's own violation *mechanism*, not just its symptom: `TestPlanVoicePlacementAppliesTheBarrierClamp`
      constructs a source-time gap that's generous (`gapLen`) but heavily timeline-compressed
      in output time, so a hold clamped only by `gapLen` still overshoots — only the
      output-time barrier (`prevPairedActionOutputEnd`) catches it. One correction made
      against the literal pseudocode while implementing: `prevPairedActionOutputEnd` updates
      to the paired *action's* own output end, not action-end-plus-audio-duration — the
      barrier is an action-ordering constraint, not audio-clip-overlap prevention between
      consecutive cues (the pseudocode's own final comment: "update prevPairedActionOutputEnd
      to this action's output end").
- [x] implement typing-pace override for the paired action's whole keystroke run —
      `computeTypingOverrides` redistributes `ttsDurationMs` across a run's real
      inter-keystroke gaps proportionally, floored at `VoiceConfig.MinKeystrokeGapMs`; plugged
      into `pause_classifier.go` via a new `classifyTypingGapOverride` and an
      `typingOverrides map[int]float64` parameter threaded through `planSegments`/`classifyGap`
      (nil on every existing call site — mechanical, behavior-preserving). Runs with no paired
      cue never appear in the map, so `classifyTypingGap`'s fixed `CollapseToMs` path is
      untouched.
- [x] add `VoiceConfig` to `config.go`: `DefaultHoldMs` (the pseudocode's `defaultHold` floor,
      default 0) and `MinKeystrokeGapMs` (the typing-redistribution floor, default 90ms) —
      follows the existing per-concern config-struct pattern.
- [x] extend `Project`/`project.json` with `Voice []PlacedVoiceCue` (`tMs`, `audioFile`,
      `durationMs`) — same shape discipline as `AudioCue`, output time only.
- [x] write regression tests: `TestDirectWithNoVoiceCuesLeavesTimelineAndSegmentsUnchanged`
      (nil vs. explicit-nil `VoiceCues` produce identical timelines) — the full existing
      `pause_classifier_test.go`/`camera_test.go`/`cursor_test.go`/`annotations_test.go` suite
      also still passes unmodified in behavior (only mechanically updated to pass `nil` for
      the new parameter).
- [x] write tests reproducing the MVP's own violation scenario and asserting the clamp fixes
      it — see above; also `TestPlanVoicePlacementClampsNaturalHoldToTheRealGapLength` for the
      source-time `gapLen` clamp specifically.
- [x] write tests for the typing-pace redistribution (proportional split, floor applied) —
      `TestComputeTypingOverridesRedistributesProportionallyToRealGapLengths`,
      `TestComputeTypingOverridesAppliesTheFloor`, `TestComputeTypingOverridesLeavesUnpairedRunsUntouched`.
- [x] run tests — `make prep` (including `-race`) passes; golden-fixture corpus in
      `internal/render` unaffected (not touched by this task, confirmed still green).

  Design decision not explicitly specified by the plan: a voice cue with no remaining
  pairable unit after it (narration after the last action) is simply left unpaired — dropped
  from placement, not an error — since it has nothing left to explain. Noted here since the
  plan doesn't call this edge case out directly.

### Task 6: Renderer — mixing variable-duration voice clips

**Files:**
- Modify: `internal/render/audio_renderer.go` (`BuildAudioFilterGraph`)
- Modify: `internal/render/audio_renderer_test.go`
- Modify: `internal/render/render.go` (wire voice clip file paths into the FFmpeg pass)

- [x] extend `BuildAudioFilterGraph` to accept the placed voice cues from `project.json` as
      an additional input group, mixed at their output `tMs` — voice clips are per-session
      files on disk (`project.Voice[i].AudioFile`), so `VisualArgs`/`Visual` (renamed from
      `RenderVisual`: touching this line surfaced a pre-existing `revive` name-stutter
      finding, fixed opportunistically per CLAUDE.md) gained a `voiceClipPaths []string`
      parameter appended as `-i` args right after click/typing, and
      `voiceInputStart = 4` is the fixed ffmpeg input index voice clips start at
      (`[4:a]`, `[5:a]`, ...), matching that ordering exactly.
- [x] confirm mixing volume/levels for voice — `voiceVolume = 1.0`, explicitly the highest
      gain of the group (vs. `musicVolume` 0.06, `clickVolume` 0.16, `typingVolume` 0.10):
      it's the narration, everything else is ambience or a sound effect under it.
- [x] write tests confirming the filter graph is byte-identical to today's when no voice
      cues are present — `TestBuildAudioFilterGraphIsByteIdenticalWithNoVoiceCues` (nil vs.
      empty slice produce identical output; asserts the graph string doesn't even mention
      "voice" when there are none).
- [x] write tests for the multi-input mixing graph shape with voice cues present, covering
      more than one scenario shape — `TestBuildAudioFilterGraphMixesVoiceCues` (lead-in hold,
      typing-run pairing, back-to-back cues at the barrier clamp) plus
      `TestBuildAudioFilterGraphVoiceInputIndexAccountsForClickTypingCues` (voice input index
      stays fixed at 4 regardless of how many click/typing sound-effect cues are also
      present, since those reuse the fixed inputs 2/3 rather than adding new ones) and
      `TestVisualArgsAppendsVoiceClipsAfterTyping`.
- [x] run tests — `make prep` passes; golden-fixture corpus in `internal/render/golden_test.go`
      confirmed still green (it compares temporal/visual filter graphs and ASS output, never
      `BuildAudioFilterGraph` — this task's own tests are what prove the audio-mixing half of
      the "inert when off" requirement, per the plan's own Testing Strategy note).

### Task 7: End-to-end wiring, failure handling, and golden-fixture regression

**Files:**
- Modify: `cmd/take5/main.go` (`analyze`/`render` read `voice.json` when present)
- Modify: `test/fixtures/corpus/` golden tests (regression check only, per Testing Strategy)

- [x] wire `analyze` to load `voice.json` when it exists next to `session.json`, pass through
      to `Direct()` — `analyzeSessionDir` in `cmd/take5/main.go`; `renderSessionDir`
      goes through the same function when `project.json` doesn't exist yet, so `render` picks
      it up too without separate wiring. Absent file means the voice-less path, unchanged —
      confirmed by manual smoke test (see below), not just by reading the code.
- [x] decide and implement the simplest reasonable failure behavior for a failed/partial
      `voice` run: **absence of `voice.json` is the failure state** — render proceeds without
      narration. This falls out of Task 4's own design rather than needing new code here:
      `synthesizeCues` only writes `voice.json` after every cue succeeds (see
      `TestSynthesizeCuesFailsLoudlyWhenRewriteDropsAnIDSilently`), so a failed or partial
      `voice` run never leaves a partial file behind — "doesn't exist" and "failed" are
      structurally the same case. `take5 voice` itself still fails loudly to stderr
      with a non-zero exit (Task 4), which is the "surfaced, not silently swallowed" half. A
      `voice.json` that exists but fails to *parse* is treated as a distinct, real error
      (analyze fails rather than silently ignoring it) — not the same as absent.
      ⚠️ **Product decision, not just an engineering default — flagged for confirmation**,
      the same way Task 1 gated on its own findings.
- [x] re-run the full existing `test/fixtures/corpus/` golden suite and confirm byte-identical
      output with no voice data — all 9 fixtures pass unchanged
      (`TestGoldenFiltersAndAssMatchTheFrozenNodeOracle`). Confirmed scope: this is the
      regression gate for the timeline/camera/cursor/annotation path (everything `Direct()`
      computes outside audio), not for audio mixing, which Task 6's own tests cover instead.
- [x] run full test suite (`make test`) — passes, including `-race`. Additionally smoke-tested
      by hand: built the binary, ran `analyze` against a hand-built session.json + voice.json
      (produced a correct `project.json` "voice" entry with the cue placed), then re-ran with
      voice.json removed (produced an empty "voice" array, no error) — confirming the wiring
      works end-to-end, not just that it type-checks.

### Task 8: `voice.wav` retention and re-run caching

**Files:**
- Modify: `internal/session` (or wherever `frames/` cleanup currently lives)
- Modify: README.md's "What ends up on disk" table

- [x] decide and implement the `voice.webm` retention policy: **kept indefinitely**, the
      opposite of `frames/`'s policy. `raw.mp4` is a lossless substitute for `frames/`, so
      deleting the JPEGs loses nothing; `voice.json` + its TTS clips are *not* a lossless
      substitute for `voice.webm` — the LLM rewrite is one-way, so re-running `voice` with a
      different model/prompt/TTS voice needs the original recording — and it's small (~15
      KB/s vs. video's two-to-three-orders-of-magnitude-larger rate). Implemented as a
      no-op — `compactSourceFrames` in `cmd/take5/main.go` simply never touches
      `session.VoiceFile`, with a comment explaining why. ⚠️ Documented in README.md's "What
      ends up on disk" as an explicitly revisable product decision, per this task's own
      framing.
- [x] ensure `take5 voice` is safe to re-run against an existing `voice.json` — defaults
      to a **no-op** (skip with a message) rather than `render`'s always-regenerate behavior,
      because unlike free/local `analyze`/`render`, a re-run here is a real LLM + TTS bill.
      `-force` opts back into redoing it. The check happens before any credential/model
      validation, so a no-op re-run needs no API key or whisper model present at all.
- [x] write tests for the retention/re-run behavior chosen —
      `cmd/take5/main_test.go`: `TestCmdVoiceIsANoOpWhenVoiceJSONAlreadyExists`
      (no API key configured; reaching the real pipeline would `os.Exit(1)` and abort the
      test binary, so returning normally is itself the assertion) and
      `TestCompactSourceFramesLeavesVoiceWebmUntouched` (frames/ removed, voice.webm kept).
      First tests this package has ever had — narrowly scoped to what's genuinely safe to
      exercise without mocking network calls.
- [x] run tests — `make prep` passes.

### Task 9: Extension UX — opt-in toggle and recording flow

**Files:**
- Modify: `extension/` popup UI
- Modify: `extension/` service worker (gate mic capture per Task 2's toggle)

- [x] add a simple opt-in control to the recording-start flow: a checkbox in
      `extension/popup/popup.html` (`#voice-enabled`), wired in `popup.js` to
      `chrome.storage.local["voiceEnabled"]` — the same key `isVoiceEnabled()` in
      `service-worker.js` already reads (Task 2). Off by default (unchecked when unset).
      Disabled outside `IDLE` phase, since the service worker only reads it once at
      recording start and changing it mid-recording would silently do nothing. **Not** the
      full history-page settings tab, per the plan's own scope note.
- [x] confirm the popup clearly communicates that enabling this sends transcript text (not
      raw audio) to external services — `.voice-disclosure` text in `popup.html`: "your
      microphone is recorded, transcribed locally, then the cleaned-up **text** (never the
      raw audio) is sent to a cloud LLM and a cloud text-to-speech service".
- [x] write tests for the popup state — confirmed there is no existing JS test framework to
      extend (`package.json` has no `test` script; matches the plan's own Testing Strategy
      note), so per that same note this is manual verification, not a gap. Done for real
      rather than skipped: a throwaway script loaded the actual `extension/` directory via
      CDP's `Extensions.loadUnpacked` (the same mechanism `test/spike/run.mjs` already uses,
      chosen specifically to avoid the native "Load unpacked" file-picker dialog, which
      browser automation can't drive), then drove the real popup and confirmed: the checkbox
      and disclosure text render; default state is unchecked with empty storage; toggling it
      on persists `{"voiceEnabled":true}` to `chrome.storage.local`; and a fresh popup
      instance (simulating reopening the popup) restores the checked state correctly.
- [x] run tests — `make prep` passes (no Go changes in this task beyond what's already
      covered; the popup verification above is the manual check this task calls for).

### Task 10: Verify acceptance criteria

- [x] verify: with the toggle off, recording/`analyze`/`render` are byte-identical to
      pre-feature behavior — the 9-fixture golden corpus passes unchanged
      (`TestGoldenFiltersAndAssMatchTheFrozenNodeOracle`), plus a manual full `analyze` +
      `render` run (see below) confirms it end-to-end, not just per-package.
- [x] verify: with the toggle on, a recorded walkthrough with narration produces a `demo.mp4`
      with synced voice — done mechanically (not audibly, since no real mic/LLM/TTS
      credentials exist in this environment): a synthetic 6s video (ffmpeg `lavfi color`) +
      hand-built `session.json`/`voice.json` + two synthetic TTS clips (ffmpeg `lavfi sine`)
      were run through the real `take5 render` binary end-to-end. It correctly
      reported "2 audio cue(s), 1 voice cue(s)" and produced a real, playable `demo.mp4`
      (57,897 bytes, 3.05s, matching the analyzed output duration) — proving the whole chain
      (director placement → renderer mixing → ffmpeg execution) actually functions, not just
      that it compiles.
- [x] verify: a cue that the LLM marks "drop" does not appear in the output and does not
      corrupt adjacent cue placement — covered by unit tests
      (`TestDirectPlacesAKeptCueAndSkipsADroppedOne`,
      `TestSynthesizeCuesFailsLoudlyWhenRewriteDropsAnIDSilently`) and by the render smoke
      test above, which included one dropped cue alongside a kept one and confirmed only the
      kept cue was mixed.
- [x] verify: an action with no paired cue is scheduled exactly as it is today —
      `TestDirectLeavesAnUnpairedActionsGapExactlyAsToday`, added specifically for this
      criterion: a *mixed* session (one action paired, one not) confirms the unpaired
      action's own timeline segments are byte-identical to the fully-voiceless case, not just
      that an all-voiceless session is unaffected.
- [x] run full test suite: `make test` — passes.
- [x] run `make lint-new` and `make prep` — both pass, `0 issues`.
- [ ] manually verify against `npm run fixture`'s acceptance app — **not done by this pass**,
      and honestly can't be: it requires a real microphone, a real OpenRouter API key, a real
      installed whisper.cpp + model, and `edge-tts`, none of which exist in this environment.
      Everything upstream of that (capture wiring, CLI plumbing, placement math, mixing) has
      been verified as thoroughly as possible without them. This item remains genuinely
      outstanding and needs the user's own environment — flagged here rather than marked done.

### Task 11: [Final] Update documentation

- [x] update README.md: added the `voice` command to the Commands table, extended the
      "What ends up on disk" table with `voice.webm`/`voice.json`/`voice/*.mp3` and the
      retention-policy paragraph (done in Task 8), and added a Privacy bullet describing the
      opt-in toggle and exactly what leaves the machine when it's on.
- [x] update README.md's opening pitch line — replaced "No Playwright, no browser replay, no
      AI, no cloud, no video editor." with a version that keeps the "no Playwright/browser
      replay/video editor" claims (still true) and explicitly calls out voice narration as
      the one opt-in, off-by-default exception that talks to the cloud.
- [x] update CLAUDE.md: added `internal/transcribe` and `internal/voiceover` to Architecture,
      updated the `cmd/take5` subcommand list and `internal/session`/`internal/director`/
      `internal/render`/`extension/` entries, and added a Conventions note confirming the
      per-package `resolveBin`/`extraBinDirs` exec-wrapper pattern is now a real convention
      (three independent occurrences: FFmpeg, whisper.cpp, edge-tts) rather than a one-off.
- [x] move this plan to `docs/plans/completed/` (done — this file).

## Technical Details

**`voice.json` cue shape** (exact fields to be finalized in Task 4, minimum contract):

```json
[
  {
    "id": "cue-0",
    "sourceStartMs": 0,
    "sourceEndMs": 9800,
    "originalText": "...",
    "rewrittenText": "...",
    "audioFile": "voice/cue-0.mp3",
    "ttsDurationMs": 5256
  }
]
```

A cue with `"rewrittenText": null` is an explicit drop and must not appear in `project.json`.

**Sequential placement pass** (Task 5, the core new algorithm):

```text
prevPairedActionOutputEnd := 0
for each (cue, action) pair in timeline order:
    gapLen        := action.sourceStart - prevAction.sourceEnd          // real room available before the action
    leadRatio     := max(0, action.sourceStart - cue.sourceStart) / (cue.sourceEnd - cue.sourceStart)
    naturalHold   := clamp(leadRatio * cue.ttsDurationMs, defaultHold, gapLen)  // reduced toward the gap, never past it
    actionOutputStart := sourceTimeToOutputTime(action.sourceStart, timeline)
    naturalStart  := actionOutputStart - naturalHold                    // the hold is what actually shifts the audio start earlier
    audioStart    := max(naturalStart, prevPairedActionOutputEnd)       // the barrier clamp
    ...place cue, update prevPairedActionOutputEnd to this action's output end
```

`naturalHold` is not a side note — it is the term that turns "how much lead-in this cue
needs" into "how much earlier than the action its audio should start." A version of this
pass that computes `naturalStart` directly from `cue.sourceStart` without folding
`naturalHold` in first is not this algorithm: it reintroduces the MVP's own bug (see MVP
findings above), because a cue's raw recorded start time is exactly what produced the
860ms violation in the spike.

This mirrors `buildTimeline`'s own sequential, single-pass style rather than introducing a
second timing model alongside it.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- Record a real walkthrough with the toggle on against `npm run fixture`'s acceptance app,
  narrating naturally (including at least one case of talking over/through an action, to
  exercise the barrier clamp for real).
- Confirm mic permission UX in Chrome doesn't surprise the user (prompt timing, what happens
  if permission is denied mid-recording).
- Listen to a full rendered `demo.mp4` end to end for pacing/naturalness, not just correctness
  of individual cue placement.

**External system / decisions to close out before or during implementation:**
- Settle the open questions listed above (mic capture specifics pending Task 1's findings,
  `voice.wav` retention, production TTS provider, `voice`-stage failure UX, multi-language
  handling) — each is called out again at the task where it becomes load-bearing.
- Provision whatever production API credentials the chosen LLM/TTS providers need; document
  them the way the project documents the FFmpeg dependency in README's Install section, not
  as anything committed to the repo.
- The history-page settings tab for toggling this feature post-recording is explicitly
  future work the user described, not part of this plan's scope.
