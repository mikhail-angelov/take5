## 2026-07-31 — Current CLI produces replay artifacts only

### Goal

Prepare a Take5 capture bundle for a product screencast.

### Golden path

1. Create a schema-v1 capture JSON with `metadata`, `steps`, `annotations`, and `debug`.
2. Run `node src/cli.js run <capture-file> --out <directory> --cut-delays`.
3. Use the generated run directory's `capture.json` and `plan.json` as the replay handoff.

### Verification

`fixtures/easycad-screencast-capture.json` completed successfully and produced both artifacts in `take5-output/easycad/`.

### Failure pattern avoided

Do not expect the current `run` command to open a browser or produce `.webm`/`.mp4`; `src/commands/run.js` currently validates, normalizes, and writes artifacts only.

### Ruled-out approaches

- Tried using `node src/cli.js run ... --cut-delays` as a video-render command; it created only `capture.json` and `plan.json` because no replay runtime is wired into the command.

### Notes

The product design describes Playwright replay and video rendering, but the checked-in implementation has not reached that stage.

## 2026-08-03 — Finalize Playwright video before resolving its path

### Goal

Render a captured Take5 scenario into a replay video without leaving the browser open indefinitely.

### Golden path

1. Stop Playwright tracing while the browser context is still open.
2. Save `page.video()` to a variable, close the context, then await `video.path()`.
3. Rename the finalized `.webm` and render or package it as the demo MP4.

### Verification

The Text2Part capture completed its authenticated replay and produced a valid 343.68-second `video.webm`; its accelerated MP4 derivative was verified by `ffprobe` (78.24 seconds, 38.8 MB).

### Failure pattern avoided

Awaiting `page.video().path()` before `context.close()` deadlocks finalization: Playwright only resolves the video path after it closes the context.

### Ruled-out approaches

- Tried resolving the video path before closing the context; the replay kept Chromium alive and never reached the renderer.
- Tried the optional `playwright-recast` cursor overlay; its FFmpeg overlay stage produced a zero-byte intermediate file for this 712-keyframe capture.

### Notes

For the affected capture, the already-generated `speed-combined.mp4` was remuxed into `demo.mp4` after the optional overlay failed.

## 2026-08-03 — Bound cursor keyframes for drag-heavy demo traces

### Goal

Render a readable cursor and input zooms for captures containing long 3D-viewer rotations.

### Golden path

1. Keep `autoZoom` before the cursor and click-effect stages in the Take5 pipeline.
2. Give `cursorOverlay` a filter that retains `click`, `mouseDown`, and `mouseUp`, but excludes every `mouseMove` sample from a drag.
3. Render the existing trace and recording; no second authenticated replay is needed.

### Verification

The Text2Part trace had 698 `mouseMove` events. Limiting the cursor to 14 click/drag-boundary keyframes produced a valid 72.82-second MP4 with a visible cursor, click effects, subtitles, and input zooms.

### Failure pattern avoided

Passing every drag movement to `playwright-recast` makes it build a deeply nested FFmpeg overlay expression. The cursor stage fails and leaves a zero-byte `cursor-overlay.mp4`.

### Ruled-out approaches

- Tried sampling drag moves at short fixed intervals; this still retained hundreds of keyframes because this trace's Playwright timestamps are sparse and irregular.
- Tried remuxing `speed-combined.mp4`; it yields a playable video but skips the cursor and zoom stages entirely.

### Notes

The recorded viewer rotation stays smooth; the synthesized cursor marks its start and end instead of attempting to redraw every movement.

## 2026-08-03 — Anchor accelerated input zooms to matching field clicks

### Goal

Keep a demo zoomed on the text field while visible typing is replayed at an accelerated timeline.

### Golden path

1. Clear replayed fields with keyboard select-all/backspace, then use `pressSequentially`, so the visible input is a single `type` trace action.
2. Map the plan's fill caption to that `type` action, rather than to a preparatory `fill` action.
3. Build an `enrichZoomFromReport` entry for every typed caption from the preceding clicked action with the same selector; let the subtitle stage provide the speed-remapped window.
4. Leave click zoom at level `1.0`, so only typing gets a zoom keyframe.

### Verification

The Text2Part trace produced exactly two input keyframes (0.36–2.25 s and 21.98–23.09 s). Extracted frames from both intervals show the active text field enlarged in place; the final MP4 is valid at 72.83 seconds.

### Failure pattern avoided

`playwright-recast` auto-zoom compares original trace action timestamps against speed-remapped subtitle timestamps. It misses typed input in an accelerated replay or associates a nearby click with the wrong window.

### Ruled-out approaches

- Tried relying on `autoZoom` alone; no input keyframe was generated because its two timestamp scales differ.
- Tried mapping a fill caption to the preceding `fill` action; the runner also emitted `type`, shifting all following captions and leaving too short a zoom window.

### Notes

The explicit report stage uses the official recast pipeline API; the preceding same-selector click supplies the field location when Playwright does not attach a point to `type`.

## 2026-08-03 — Preserve minimum caption time after speed remapping

### Goal

Render readable narration captions for a replay whose idle periods are accelerated.

### Golden path

1. Record a real-time `waitForTimeout` of at least 3100 ms before every captioned action.
2. Keep those waits at speed `1.0` in the speed map.
3. Generate source SRT cues from those waits, remap them with the trace speed map, then burn the resulting video-time SRT onto the rendered MP4.
4. Verify the SRT's shortest cue and the MP4 duration with `ffprobe`.

### Verification

The Text2Part replay rendered to a valid 98.53-second MP4 with 11 cues; the measured shortest cue duration was exactly 3000 ms.

### Failure pattern avoided

`playwright-recast`'s direct subtitle stage can retain trace-relative timing after video trimming, making the opening cue too short. Its zoom pass can also inherit an invalid nominal FPS from concatenated speed segments and yield a sub-second MP4.

### Ruled-out approaches

- Tried burning the SRT produced directly by the recast subtitle stage; the first cue was only 738 ms.
- Tried the recast zoom stage for this accelerated replay; FFprobe reported a 0.64-second MP4 despite a full-sized file.

### Notes

Use the trace speed map and first recording-frame offset when turning source dwell timestamps into final-video SRT timestamps.
