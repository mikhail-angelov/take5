# Voice annotation sync fix + freeze-frame stretching

## Overview

Voice annotations placed by `internal/director/voice.go` (`docs/plans/completed/2026-08-19-voice-annotations.md`)
can currently drift out of sync with the actions they narrate, two ways:

1. **Consecutive cue audio can overlap each other.** `planVoicePlacement`'s barrier
   (`voice.go:174-177`) only clamps against the previous *paired action's* output end, never
   against the previous *cue's own audio* output end. Task 5 of the original plan's own
   implementation note names this a deliberate choice ("the barrier is an action-ordering
   constraint, not audio-clip-overlap prevention between consecutive cues"); the plan's MVP
   findings separately note TTS narration typically comes out shorter than the raw speech it
   replaces. This plan reads those two notes together as the likely reasoning, though the
   original text never states the link explicitly — worth a quick confirmation with whoever
   wrote it if the history matters. Regardless of the original reasoning, that assumption
   doesn't always hold in practice.
2. **No mechanism exists to make room when a cue's audio needs more room than the timeline
   naturally offers.** The placement pass can only clamp/compress today, never extend the
   video. The fix is a **freeze-frame**: hold the last real frame for exactly the overrun.

Both causes turn out to be two failure modes of the same underlying gap: the placement pass
has never had a real *ceiling* (only a floor), and never had a way to buy more room when the
floor and ceiling conflict. This plan replaces the placement formula with one that has both,
plus the timeline/render support the ceiling case (freeze) requires.

## Context (from discovery)

- `internal/director/voice.go` — `pairVoiceCues`/`computePairableUnits` (cue↔action 1:1
  pairing — unaffected by this plan, already correct) and `planVoicePlacement` (rewritten by
  this plan).
- `internal/director/timeline.go` — `RawSegment`/`Segment`, `buildTimeline`,
  `sourceTimeToOutputTime`, `isSourceTimeKept`. Every segment today has `Speed > 0` and derives
  `OutputEnd - OutputStart = (SourceEnd - SourceStart) / Speed` — a held/frozen instant
  (source doesn't advance, output does) is **not representable**. This is the actual blocker
  for freeze-frame support.
- `internal/render/temporal_renderer.go` — `BuildTemporalFilterGraph` (Pass A) explicitly
  rejects any segment with `Speed <= 0` or `SourceEndMs <= SourceStartMs`
  (`temporal_renderer.go:32-38`). Must be relaxed specifically for held segments.
- **Traced and ruled out as needing changes:** `internal/director/camera.go` and `cursor.go`
  only ever call `sourceTimeToOutputTime(action.StartMs/EndMs, timeline)` — they never iterate
  `timeline` segments directly. `internal/render/visual_renderer.go` and `ass.go` never touch
  `project.Timeline` at all (only `render.go` does, feeding it straight into
  `BuildTemporalFilterGraph`). So once `sourceTimeToOutputTime` correctly maps an instant
  across an inserted hold (Task 1), camera/cursor/annotations/audio automatically shift with
  it — **zero code changes needed in those files**, only regression tests (Task 4).
- `internal/director/config.go` — existing `VoiceConfig{DefaultHoldMs, MinKeystrokeGapMs}`.
  `DefaultHoldMs` keeps its role (minimum forced lead-in before the annotated action) under the
  new formula too.
- `internal/director/director.go` — today calls `timeline := buildTimeline(plan.Segments)`
  and then `planVoicePlacement(voicePairings, timeline, config.Voice)`. This plan changes that:
  `planVoicePlacement` now *produces* the final timeline (it may extend `plan.Segments` with
  held inserts as it goes), so `Direct()` must take the timeline it returns rather than
  building one itself beforehand.
- `test/fixtures/corpus/` — golden fixtures (`session.json` → byte-identical `project.json`/
  filter-graphs/ASS). None of the 9 existing fixtures carry voice cues, so this plan is inert
  against them by construction; a new fixture case covers the freeze path (Task 5). Per
  CLAUDE.md, goldens are hand-verified once and then frozen, not auto-regenerated.

## Decisions locked in (from this conversation — not reopened by this plan)

⚠️ **Amended after this plan was completed** (separate follow-up conversation, same day): the
bullet below originally read "next action" as "the next *paired* unit only," matching the
precedent `prevPairedActionOutputEnd` set. In practice that left every *silent* (unpaired)
action between two narrated ones completely unprotected — a cue could legitimately talk
straight over an action nobody was narrating, with nothing to tell "what's being narrated"
apart from "what happens next." Both ceiling and floor were rescoped to look at the very next/
previous action **overall**, paired or not — see `internal/director/voice.go`'s own doc
comment on `planVoicePlacement` for the current, authoritative version; the pseudocode and
bullets below are left as originally written for history, with the correction called out
inline rather than silently rewritten.

⚠️ **Amended again, same follow-up conversation:** two more gaps in the original algorithm,
both at the edges of a session:

- **The very first action in the whole session is now its own ceiling too** — every other
  action can be legitimately talked over (something is already on screen), but nothing
  precedes the first one, so an intro cue's narration must finish before it, not during it.
  Implemented as a per-pairing exception (`unitIdx == 0`) in `planVoicePlacement`, not a
  general rule — freezing the action's own reveal (not the next one's) when it doesn't fit.
- **A trailing cue (narration after the last recorded action, e.g. a wrap-up) is no longer
  dropped.** `pairVoiceCues` used to `break` once units ran out, silently discarding every
  later cue; `voicePairing.Unit` is now `*pairableUnit` (nil for "nothing left to explain"),
  and `planVoicePlacement` places a trailing cue immediately after whatever came before it,
  extending the video with a tail freeze to fit it — the same mechanism as every other freeze,
  anchored at the current end of the timeline instead of a specific next action.

The full placement algorithm, per pairing `i` (cue → unit), looking at the *next* pairing
`i+1` if one exists:

```
annotatedStartOut = sourceTimeToOutputTime(unit_i.StartMs, timeline)
nextStartOut      = sourceTimeToOutputTime(unit_{i+1}.StartMs, timeline)   // +inf if no pairing i+1
floor             = max(prevPairedActionOutputEnd, prevCueAudioOutputEnd)
ttsDur            = cue_i.TTSDurationMs

candidateStart = sourceTimeToOutputTime(cue_i.SourceStartMs, timeline)  // where they actually started talking
candidateEnd   = candidateStart + ttsDur

target := candidateStart
if candidateEnd > nextStartOut:            target = nextStartOut - ttsDur       // would talk over the NEXT action — pull earlier
else if candidateEnd < annotatedStartOut:  target = annotatedStartOut - ttsDur  // would finish before its OWN action — push later, no dead air
target = min(target, annotatedStartOut - DefaultHoldMs)  // guaranteed minimum lead-in

audioStart = max(target, floor)
audioEnd   = audioStart + ttsDur

if audioEnd > nextStartOut:
    // even after pulling as early as the floor allows, still overruns — freeze the NEXT
    // paired action's reveal until audioEnd (holdMs = audioEnd - nextStartOut), then continue
```

**Superseded — see the amendment above:** `nextStartOut`/`prevPairedActionOutputEnd` in the
pseudocode now mean "the next/previous action overall" (`units[unitIdx±1]`, the full
`computePairableUnits` sequence), not "the next/previous *paired* pairing." The shape of the
algorithm (ceiling, floor, freeze-on-conflict) is otherwise unchanged.

Key points this locks in:

- **Overlapping the cue's own annotated action is fine and expected** — real narration often
  keeps talking while the action it describes happens. The ceiling is the *next* action's
  reveal, not the current one. This replaces the old `leadRatio`/`cueSpanMs`-proportional
  partial hold entirely — the new rule always aims for "audio ends exactly at the boundary it's
  pushing against," never a proportional fraction.
- ~~**"Next action" means the next *paired* unit** (`unit_{i+1}` from the next pairing),
  matching the existing precedent that `prevPairedActionOutputEnd` only ever tracks paired
  actions. Silent (unpaired) actions between two paired ones are not protected from narration
  overlap.~~ **Superseded** — see the amendment above: "next/previous action" now means any
  action, paired or not.
- **`DefaultHoldMs` keeps its job**: a floor guaranteeing at least that much lead-in before the
  annotated action, applied on top of the ceiling/floor logic above.
- **Freeze visual: a hard stop-frame.** Camera and cursor hold their exact pre-freeze state —
  nothing animates *into* a held instant because no real action exists inside it.
- **No cap on freeze length.** Always stretch enough to fit the full overrun, however long.
- **Freeze applies uniformly to every pairing**, typing-run or not — it's a property of the
  cue's own audio placement, orthogonal to `computeTypingOverrides` (which only reshapes the
  *internal* keystroke gaps of a paired typing run and runs before `buildTimeline`, unaffected
  by this).
- **Placement must be incremental, not a one-shot pre-pass.** Because a freeze inserted for
  pairing `i` shifts `nextStartOut` for pairing `i+1` (and everything after), freeze insertion
  can't be computed as a batch of `RawSegment`s handed to a single `buildTimeline` call the way
  `computeTypingOverrides` is. `planVoicePlacement` must process pairings strictly in order,
  rebuilding the timeline via `buildTimeline` immediately after any insert, before computing
  the next pairing's `nextStartOut`.

## Development Approach

- **Testing approach:** regular — implement each task's code, then write/update its tests, per
  `make prep`'s existing loop.
- Complete each task fully (including its tests, green) before starting the next.
- `internal/director` and `internal/render` stay pure/deterministic — every quantity above
  derives solely from cue/action/timeline data, never wall-clock or I/O.

## Testing Strategy

- Unit tests for every task, table-driven `testing.T`, no third-party assertion library.
- Character-exact filter-graph assertions for Task 3 (`temporal_renderer.go`), matching the
  existing convention flagged in that file's own comments (`seconds()`'s `toFixed(6)` trap).
- Golden-fixture regression (`test/fixtures/corpus`,
  `TestGoldenFiltersAndAssMatchTheFrozenNodeOracle`) must stay byte-identical for all 9
  existing (voice-less) fixtures throughout every task — the standing proof this plan is fully
  inert with no voice data.
- One new fixture case added in Task 5 to cover the freeze path end-to-end.

## What Goes Where

- **Implementation Steps** (checkboxes below): all code changes, tests, and fixture updates —
  everything is achievable inside this repo.
- **Post-Completion**: none required — pure backend/library change, no UI, nothing external.

## Implementation Steps

### Task 1: Extend the timeline model with a held (freeze) segment — DONE

**Files:**
- Modify: `internal/director/timeline.go`
- Modify: `internal/director/timeline_test.go`
- Modify: `internal/director/director.go` (`TimelineSegment` JSON shape lives here, not
  `types.go` — corrected during implementation; `tlOut` construction)

- [x] add `HoldMs float64` to `RawSegment` and `Segment`: a held segment has
      `SourceStartMs == SourceEndMs` (a true instant) and `HoldMs > 0` gives its output
      duration directly, bypassing the `(SourceEnd-SourceStart)/Speed` formula entirely
- [x] `buildTimeline`: a held raw segment bypasses the `epsilonMs` zero-width filter (its zero
      source width is legitimate, not a degenerate input), is never merged with a neighboring
      segment even if contiguous and same-speed, and gets `OutputEndMs = OutputStartMs + HoldMs`
- [x] fix `sourceTimeToOutputTime`'s tie-breaking so a held segment actually wins the boundary
      instant it shares with the preceding segment. **Auto-review finding, fixed:** the normal
      segment's own `==SourceEndMs` branch now looks ahead one segment and defers (`continue`)
      when the next segment is a held one starting at the same instant; the held segment's own
      branch then returns `OutputEndMs` (post-hold). Paired with a `buildTimeline` sort
      tie-break (held segments always sort before a same-instant real segment) so the lookahead
      always finds the held segment adjacent, not several entries away.
- [x] fix `buildTimeline` so a held segment survives both places that currently treat
      zero-width input as noise. **Auto-review finding, fixed:** held segments now get an
      entirely separate branch in the merge loop (appended directly, before the overlap-clip
      check ever runs) and the merge-into-previous check gained a `last.HoldMs <= 0` guard, so
      a held segment can never be silently dropped or merged away.
- [x] `isSourceTimeKept`: a held instant counts as kept (not cut) — true by construction (no
      code change needed, confirmed by test)
- [x] `TimelineSegment` gained `HoldMs int64 \`json:"holdMs,omitempty"\`` (in `director.go`,
      where the struct actually lives); wired through `director.go`'s `tlOut` construction
      (`math.Round`'d like `SourceStartMs`/`SourceEndMs`); absent/zero for every segment today,
      so `project.json` is unchanged for any session with no freezes
- [x] tests added: `TestBuildTimelineInsertsAHeldSegmentWithoutMergingAcrossIt`,
      `TestBuildTimelineNeverMergesAHeldSegmentWithANeighbor`,
      `TestSourceTimeToOutputTimeResolvesAHeldBoundaryToPostHold`,
      `TestIsSourceTimeKeptIsTrueAtAHeldInstant`
- [x] confirmed the full existing `timeline_test.go` suite (no held segments) passes unchanged
- [x] `make prep` passes (full suite + `-race`, lint-new clean)

➕ **Found during implementation, beyond the auto-review's findings:** a held instant very
often lands in the *middle* of an already-wide real segment (e.g. one flat segment spanning
the whole recording) rather than at a pre-existing boundary between two `RawSegment`s —
`planVoicePlacement` has no way to know how `planSegments` happened to chunk the surrounding
footage. `buildTimeline` needed an actual split: the containing segment is truncated at the
hold point and its remaining tail re-appended as a new segment after the hold, via a new
`insertHeldSegment` helper (pulled out of the merge loop to keep `nestif` lint happy). Covered
by `TestBuildTimelineSplitsAWideSegmentAroundAHeldInstantInsideIt`.

### Task 2: Rewrite `planVoicePlacement` — sequential ceiling/floor placement with incremental freeze insertion — DONE

**Files:**
- Modify: `internal/director/voice.go` (`planVoicePlacement`, new signature and body)
- Modify: `internal/director/director.go` (call site — takes the timeline `planVoicePlacement`
  returns instead of building one beforehand)
- Modify: `internal/director/voice_test.go`

- [x] changed `planVoicePlacement`'s signature from
      `(pairings []voicePairing, timeline []Segment, vc VoiceConfig) []PlacedVoiceCue` to
      `(pairings []voicePairing, rawSegments []RawSegment, vc VoiceConfig) ([]Segment, []PlacedVoiceCue)`
      — it now owns building (and, when needed, extending) the timeline
- [x] implemented the loop per the "Decisions locked in" pseudocode: `annotatedStartOut`/
      `nextStartOut` computed off the *current* timeline each iteration,
      `candidateStart`/`target`/`audioStart`/`audioEnd` per pairing, held `RawSegment` inserted
      at `pairings[i+1].Unit.StartMs` with `HoldMs = audioEnd - nextStartOut` and the timeline
      rebuilt via `buildTimeline` before moving to pairing `i+1` when it overruns
- [x] last-pairing edge case decided and implemented: `nextStartOut = +Inf` for the final
      pairing, so its audio is simply allowed to run past the current end of the timeline —
      no tail freeze invented for it. Pinned by
      `TestPlanVoicePlacementLastPairingHasNoCeilingAndNoTailFreeze`.
- [x] `prevPairedActionOutputEnd`/`prevCueAudioOutputEnd` bookkeeping reads the possibly-just-
      rebuilt timeline
- [x] `Direct()` in `director.go` updated: `timeline, voice := planVoicePlacement(voicePairings, plan.Segments, config.Voice)`
      now runs before cursor/camera/annotations/audio, which all take this timeline
- [x] deleted the `leadRatio`/`cueSpanMs`-based partial-hold code path and the `max0` helper
      (confirmed unused elsewhere)
- [x] tests added, one per branch: `TestPlanVoicePlacementFitsNaturallyWithNoAdjustment`,
      `TestPlanVoicePlacementPushesLaterToAvoidDeadAirBeforeItsOwnAction`,
      `TestPlanVoicePlacementPullsEarlierToAvoidTalkingOverTheNextActionNoFreezeNeeded`,
      `TestPlanVoicePlacementInsertsAFreezeWhenTheFloorForcesAnOverrun` (exact `HoldMs` and the
      next action's shift both asserted), `TestPlanVoicePlacementHandlesBackToBackFreezesAcrossThreeConsecutiveCues`,
      `TestPlanVoicePlacementAppliesFreezeUniformlyToATypingRunPairing`,
      `TestPlanVoicePlacementLastPairingHasNoCeilingAndNoTailFreeze`
- [x] `TestPlanVoicePlacementWithNoPairingsReturnsTheSameTimelineBuildTimelineWouldAlone` —
      the "inert when off" regression
- [x] `make prep` passes (full suite + `-race`, lint-new clean); golden-fixture corpus
      unaffected

➕ **Found while writing Task 2's own tests:** the very first hand-worked freeze scenario
failed because the test's `rawSegments` was one flat segment spanning the whole timeline (the
realistic shape) rather than pre-split at the freeze point — which is exactly the gap Task 1's
addendum above (`insertHeldSegment`'s split) exists to cover. Confirms the split was not a
hypothetical edge case but the ordinary path.

### Task 3: Render Pass A — actually render held segments as a frozen clip — DONE

**Files:**
- Modify: `internal/render/temporal_renderer.go` (`BuildTemporalFilterGraph`, new
  `segmentFilterChain` helper)
- Create: `internal/render/temporal_renderer_test.go` (no such file existed — the function was
  previously only exercised indirectly via `golden_test.go`)

- [x] relaxed the `Speed <= 0`/`SourceEndMs <= SourceStartMs` validation to skip both checks
      when `HoldMs > 0`; unchanged (still a hard error) for any non-held segment
- [x] held segments now emit `trim=start=...:end=...,setpts=PTS-STARTPTS,tpad=stop_mode=clone:stop_duration=X`
      instead of the ordinary `trim+setpts/speed` line, factored into a shared
      `segmentFilterChain` helper used by both the single- and multi-segment code paths.
      **Auto-review finding, fixed:** `X = (HoldMs - 1000/fps) / 1000`, clamped to zero when
      `HoldMs < 1000/fps`, not `HoldMs/1000` — confirmed against hand-derived expected output
      (`frameMs = 1000/30 = 33.333333ms`) before pinning the test string, not the other way
      round
- [x] single-segment special case (`len(timeline) == 1`) now also routes through
      `segmentFilterChain` (reading from `[src]` rather than a split `[sN]` label) — covered
      by `TestBuildTemporalFilterGraphRendersALoneHeldSegment` for the degenerate
      lone-held-segment case
- [x] tests added: `TestBuildTemporalFilterGraphRendersAHeldSegmentAsAFrozenClip` (character-exact,
      mixed normal+held, multi-segment case), `TestBuildTemporalFilterGraphRendersALoneHeldSegment`,
      `TestBuildTemporalFilterGraphStillRejectsAMalformedNonHeldSegment`,
      `TestBuildTemporalFilterGraphStillRejectsAnInvalidSpeedOnANonHeldSegment`,
      `TestBuildTemporalFilterGraphSkipsValidationForAHeldSegmentsSpeedAndSpan`
- [x] `TestGoldenFiltersAndAssMatchTheFrozenNodeOracle` (no held segments in any existing
      fixture) stays byte-identical — confirmed by `make prep`
- [x] `make prep` passes (full suite + `-race`, lint-new clean)

### Task 4: Regression — confirm camera/cursor/annotations/audio need no code changes — DONE

**Files:**
- Modify: `internal/director/camera_test.go`
- Modify: `internal/director/cursor_test.go`
- Modify: `internal/director/annotations_test.go`
- Modify: `internal/director/audio_test.go`

- [x] `TestPlanCursorNeedsNoCodeChangeForAFreeze`, `TestPlanAnnotationsNeedsNoCodeChangeForAFreeze`,
      `TestPlanAudioNeedsNoCodeChangeForAFreeze` — all confirm a held segment shifts the
      action-after-the-freeze's output time by exactly `HoldMs` and leaves the
      action-before-the-freeze untouched, with zero changes to `cursor.go`/`annotations.go`/`audio.go`
- [x] `TestPlanCameraNeedsNoCodeChangeForAFreeze` — same claim, but discovered mid-writing that
      the naive "every keyframe at or after the hold's output instant shifts by exactly
      `HoldMs`" version of this test is **wrong**, not because camera.go needs code changes,
      but because `planCamera`'s own `shotRange` windowing (`camera.go` around line 161-164)
      clips a shot's trailing hold against the *next* shot's start via `math.Min`/`math.Max` —
      an interior keyframe's `TMs` can be a constant derived purely from its OWN target
      instant (e.g. `targetA.TMs + TrailMs`) that happens to already be earlier than the next
      shot's window, in which case it's legitimately unaffected by the freeze even though it
      numerically falls after the hold's output point. Confirmed this is correct (not a
      regression) by hand-tracing `camera.go`'s window-clip arithmetic against printed
      before/after keyframe dumps. The test now only asserts what's safe to assert from
      outside that internal windowing: the very first keyframe (upstream of everything) is
      untouched, and the very last keyframe (downstream of the last action) shifts by exactly
      `HoldMs` — still zero changes to `camera.go` itself, just a more honest assertion surface
- [x] `make prep` passes (full suite + `-race`, lint-new clean); no code changes were needed in
      any of the four files, confirming this task's own title

### Task 5: End-to-end regression — DONE

**Scope revised during implementation:** the plan originally proposed a new
`test/fixtures/corpus/` fixture, matching that directory's existing structure. Re-reading
`golden_test.go`'s own header comment while implementing this task: that corpus exists
specifically to prove Go's output matches a **frozen Node oracle**, character for character —
a port-fidelity check. Freeze-frame support has no Node counterpart to match (it never existed
in the original JS implementation), so a corpus entry for it wouldn't be testing what that
mechanism exists to test, and per CLAUDE.md, "New Go-only behavior gets ordinary table-driven
`testing` tests" — which Tasks 1-4 already provide in exhaustive, hand-verified, exact-value
form (`voice_test.go`, `timeline_test.go`, `temporal_renderer_test.go`, the four
`*NeedsNoCodeChangeForAFreeze` tests). Adding a parallel golden-corpus entry (with its own
`gen-fixtures.mjs` scaffolding) would duplicate that coverage without adding a new kind of
protection. Replaced with a single focused end-to-end `Direct()` integration test instead.

**Files:**
- Modify: `internal/director/voice_test.go`

- [x] confirmed all 9 existing `test/fixtures/corpus` fixtures remain byte-identical (proven by
      every `make prep` run since Task 1; re-confirmed here)
- [x] added `TestDirectSyncsOverlappingCuesAndAFreezeEndToEnd`: combines both original
      symptoms in one `Session` run through `Direct()` (not the lower-level
      `planVoicePlacement` directly) — a cue with a deliberately huge `TTSDurationMs` (5000ms)
      paired to the first click, a short cue paired to the second. Since exact tMs values
      depend on `planSegments`' own gap-compression math (not hand-derived here), the test
      asserts invariants rather than pinned numbers: `Project.Voice[1].TMs` never starts before
      `Project.Voice[0]`'s own audio end (no cue-to-cue overlap), `Project.Timeline` contains
      at least one held segment (the freeze fired), and `Project.Cursor.Clicks[1].TMs` (the
      second action's own reveal) lands at or after cue-0's audio end (freeze correctly delayed
      the action, not just the audio)
- [x] `make prep` passes (full suite + `-race`, lint-new clean)

### Task 6: [Final] Verify acceptance criteria and update documentation — DONE

- [x] verified: consecutive voice cues never overlap in output time —
      `TestPlanVoicePlacementInsertsAFreezeWhenTheFloorForcesAnOverrun`,
      `TestPlanVoicePlacementHandlesBackToBackFreezesAcrossThreeConsecutiveCues`,
      `TestDirectSyncsOverlappingCuesAndAFreezeEndToEnd`
- [x] verified: a cue whose placement would talk over the next action either fits after
      pulling earlier (`TestPlanVoicePlacementPullsEarlierToAvoidTalkingOverTheNextActionNoFreezeNeeded`)
      or triggers a freeze (`TestPlanVoicePlacementInsertsAFreezeWhenTheFloorForcesAnOverrun`)
      — never an overlap, never a silently-truncated narration
- [x] verified: sessions with no voice cues are byte-identical to pre-plan output, at every
      stage — `TestDirectWithNoVoiceCuesLeavesTimelineAndSegmentsUnchanged`,
      `TestDirectLeavesAnUnpairedActionsGapExactlyAsToday`,
      `TestPlanVoicePlacementWithNoPairingsReturnsTheSameTimelineBuildTimelineWouldAlone`, and
      all 9 `test/fixtures/corpus` fixtures (`project.json`/filter graphs/ASS) staying
      byte-identical across every task's `make prep` run
- [x] `internal/director/voice.go`'s top-of-file doc comment updated to describe the new
      structure (`planVoicePlacement` now owns timeline construction, not just placement) and
      to point at this plan's own path once moved to `docs/plans/completed/`
- [x] `make prep` passes (full suite + `-race`, lint-new clean)
- [x] plan moved to `docs/plans/completed/`

## Technical Details

**Held segment shape** (`timeline.go`):

```
RawSegment{ SourceStartMs: t, SourceEndMs: t, HoldMs: h }   // Speed is irrelevant/ignored
Segment{   SourceStartMs: t, SourceEndMs: t, HoldMs: h,
           OutputStartMs: o, OutputEndMs: o + h }
```

**Full placement loop** (`planVoicePlacement`, see "Decisions locked in" for the per-pairing
formula):

```
timeline := buildTimeline(rawSegments)
for i, p := range pairings {
    ... compute annotatedStartOut, nextStartOut, candidateStart/End, target, audioStart, audioEnd
    if audioEnd > nextStartOut && i+1 < len(pairings) {
        rawSegments = append(rawSegments, RawSegment{
            SourceStartMs: pairings[i+1].Unit.StartMs,
            SourceEndMs:   pairings[i+1].Unit.StartMs,
            HoldMs:        audioEnd - nextStartOut,
        })
        timeline = buildTimeline(rawSegments)
    }
    ... append PlacedVoiceCue, update prevPairedActionOutputEnd/prevCueAudioOutputEnd
}
return timeline, placed
```

**Known edge case, must be resolved as part of Task 1's tie-breaking fix (see below), not
deferred:** if `unit_i.EndMs == unit_{i+1}.StartMs` (back-to-back actions with no gap) and a
freeze gets inserted at that shared instant, `sourceTimeToOutputTime(unit_i.EndMs)` will also
resolve post-hold — `prevPairedActionOutputEnd` for pairing `i` would then read as if the
freeze applied to *its* action too. Confirm whether this is correct (arguably it is — the
freeze genuinely delays everything from that source instant onward) or needs a distinct
pre/post-hold query.

**`sourceTimeToOutputTime` tie-breaking (Task 1 — auto-review finding, load-bearing):** the
current implementation returns on the *first* segment satisfying `sourceMs <= SourceEndMs` in
iteration order. Since a held segment's `SourceStartMs` is always exactly the preceding kept
segment's `SourceEndMs`, that preceding segment matches first and the function returns the
pre-hold time — the held segment's own branch is never reached for the boundary instant it
exists to affect. Held/zero-width segments must be special-cased to win ties at their own
boundary instant.

**`buildTimeline` held-segment guards (Task 1 — auto-review finding, load-bearing):** besides
the leading `epsilonMs` zero-width filter, two more places treat zero-width input as noise and
must gain `HoldMs > 0` guards: the overlap-clip check
(`segment.SourceEndMs-sourceStartMs <= epsilonMs { continue }`) and the same-speed/contiguous
merge-into-previous check — both otherwise silently drop or absorb a held segment before it
ever reaches the output-time assignment loop.

**FFmpeg filter chain for a held segment** (`temporal_renderer.go`), replacing the normal
`trim=start=...:end=...,setpts=.../speed` line for that segment index:

```
[s%d]trim=start=%s:end=%s,setpts=PTS-STARTPTS,tpad=stop_mode=clone:stop_duration=%s[v%d]
```

where the trim window is one frame wide (`1/fps` seconds at the hold's source instant) and
`stop_duration` is `(HoldMs - 1000/fps) / 1000` seconds — **not** `HoldMs/1000` (auto-review
finding: the trimmed frame itself already contributes `1/fps` of output duration, so using
`HoldMs/1000` directly would make the rendered segment one frame longer than the timeline
claims, drifting real video length from `project.json`'s durations as freezes accumulate).
Clamp to a zero pad when `HoldMs < 1000/fps`.

## Post-Completion

None — self-contained backend/library change (`internal/director`, `internal/render`) with no
UI, no consuming project, and no deployment or config surface to update.
