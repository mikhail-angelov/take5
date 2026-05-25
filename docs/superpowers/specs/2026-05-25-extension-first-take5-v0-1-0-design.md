# Take5 v0.1.0 Extension-First Design

## Overview

Take5 v0.1.0 should shift from a prompt-first demo recorder to an extension-first capture and replay product.

The primary workflow is:

1. The user installs a Chrome extension and the `take5` CLI.
2. The user opens their web app and manually performs the target flow while the extension captures semantic actions.
3. The user optionally adds inline annotations to important elements during capture.
4. The extension saves scenarios locally and can export them as JSON.
5. The user runs `npx take5 run <capture-file>` to normalize the capture into a replay plan, replay it in Playwright, and record a polished demo video plus failure artifacts.

This architecture keeps flow discovery human-driven and recording/polish machine-driven.

## Goal

Ship a polished local-first `v0.1.0` where a user can manually capture a product flow in Chrome, export that capture, and generate a demo video with useful artifacts through the `take5` CLI.

## Target Users

- Product managers creating walkthroughs
- Founders preparing quick product demos
- Sales engineers capturing tailored flows
- Developer advocates documenting workflows
- Internal teams recording release demos

## Success Criteria

- A user can capture a web-app flow through the Chrome extension without authoring selectors or scripts.
- A user can export a saved capture and run `npx take5 run <capture-file>` to produce a demo video locally.
- Failed runs include actionable artifacts: saved replay plan, step log, screenshots, console errors, network errors, and summary.
- Inline annotations captured in the extension appear as structured notes that can influence replay captions or callouts.

## Non-Goals

- Prompt-first generation as the main `v0.1.0` workflow
- Cloud sync, team collaboration, or remote job execution
- Guaranteed deterministic replay across all dynamic web apps
- Cross-browser capture beyond Chrome for `v0.1.0`
- A full visual editor for scenarios
- A web dashboard or hosted video library

## Product Flow

### Capture

1. The user opens the target app in Chrome.
2. The user opens the Take5 extension UI and starts scenario capture.
3. The extension records semantic actions such as navigation, click, fill, select, scroll, key press, wait, and optional assertions or notes.
4. At any point the user can hold `Ctrl` to enter temporary annotation mode, click an element, and attach a text comment.
5. The user stops capture and saves the scenario into extension storage.
6. The user can optionally export the scenario to a JSON file.

### Validate

1. The user opens the saved scenario list inside the extension.
2. The user can replay the scenario inside the extension in either auto-play mode or manual `Next/Back` mode.
3. During replay, saved annotations reappear on the associated elements so the user can validate captured context.

### Render

1. The user runs `npx take5 run <capture-file> --out <dir>`.
2. The CLI validates the capture file and normalizes it into a replay plan.
3. The CLI launches an isolated Playwright session, executes the plan, records the browser session, and captures artifacts.
4. The CLI post-processes the output into a polished video, including highlights, labels, captions, `.mp4` conversion when available, and idle trimming.

## Architecture

### Chrome Extension

The extension is the primary capture surface. It is responsible for:

- starting and stopping scenario capture
- observing user actions and converting them into semantic steps
- collecting raw DOM and user-event data as debug metadata
- handling inline annotations
- persisting scenarios in extension-local storage
- exporting and importing scenario JSON
- replaying saved scenarios inside the browser for validation

The extension is not responsible for final video rendering.

### Capture Schema

The extension exports a versioned JSON capture bundle that becomes the contract between capture and replay.

The schema has four layers:

- `metadata`
  Scenario id, name, timestamps, base URL, schema version, viewport preset, and extension version.
- `steps`
  High-level semantic actions such as `navigate`, `click`, `fill`, `select`, `keypress`, `scroll`, `wait`, and `assert_text`.
- `annotations`
  User-authored notes anchored to a page URL and element locator, with text, order index, and optional relation to the nearest step.
- `debug`
  Raw DOM/user-event stream, candidate selectors, page title, and other evidence useful for replay debugging.

### CLI Pipeline

The CLI should use an explicit pipeline:

1. input validation
2. capture import
3. capture normalization
4. replay-plan generation
5. Playwright execution
6. artifact collection
7. video post-processing
8. summary output

The default user-facing command for `v0.1.0` is `take5 run <capture-file>`.

### Replay Runtime

The replay runtime owns:

- isolated Playwright browser context
- viewport presets
- per-step timeout policy
- screenshot capture on failure
- console and network error listeners
- clean shutdown and artifact flushing

## Extension UX

### Floating Toolbar

The extension should provide a floating toolbar inspired by the interaction model of `agentation`, but implemented independently.

The toolbar should appear in the bottom-right area of the page and expose:

- `Start`
- `Stop`
- `Save`
- `Replay`
- `Scenarios`
- `Delete`

The toolbar should also show current capture state and a lightweight count of recorded steps and annotations.

Reference: the `agentation` project describes a bottom-right toolbar and click-to-annotate model. Take5 should use this only as a UX reference and not reuse code directly because that project is separately licensed. Source: https://github.com/benjitaylor/agentation

### Annotation UX

Annotation behavior is part of the MVP:

- Holding `Ctrl` enters temporary annotation mode.
- The cursor changes to an add-annotation state.
- Clicking an element opens a compact text popover.
- Saving the text creates a visible marker or icon pinned to that element.
- The marker remains visible during capture after save.
- Saved markers reappear during extension replay for that scenario.

Annotations are not replay actions. They are semantic notes that later inform captions, labels, or callouts during CLI recording.

### Scenario Manager

The extension should provide a scenario manager with:

- list saved scenarios
- select a scenario
- replay a scenario
- export a scenario to JSON
- import a scenario from JSON
- delete a scenario

Storage model for `v0.1.0` is dual:

- extension-local storage for convenience
- JSON export/import for CLI handoff and portability

### Replay UX in Extension

The extension replay feature is a validation layer, not the final recorder.

It should support both:

- fully automatic step-by-step replay
- manual step-through replay with `Next` and `Back`

Replay should use semantic steps, not raw DOM events, to avoid excessive fragility.

## Scenario and Annotation Semantics

### Supported Semantic Steps

The initial capture and replay vocabulary should include:

- `navigate`
- `click`
- `fill`
- `select`
- `keypress`
- `scroll`
- `wait`
- `assert_text`

This list should stay intentionally small for `v0.1.0`.

### Annotation Anchoring

Each annotation should store:

- page URL at creation time
- primary semantic locator
- fallback selectors
- annotation text
- creation timestamp
- order index
- optional nearest-step id

This is enough to restore annotations during extension replay and convert them into replay-time captions or labels during CLI recording.

## CLI Scope for v0.1.0

The CLI should support:

- `npx take5 run <capture-file>`
- output directory selection
- structured artifact emission
- Playwright-based replay
- video recording
- `.mp4` conversion when ffmpeg is available
- idle trimming

Provider selection, prompt-first generation, and broader command surface can be added later. They are not required to validate the extension-first product.

## Artifacts

Each CLI run should produce a structured output directory containing:

- original capture file copy
- normalized replay plan
- step execution log
- screenshots
- console errors
- network errors
- summary
- raw browser video
- converted `.mp4` when available

These artifacts are necessary to satisfy the requirement that failures be actionable rather than opaque.

## Reliability and Constraints

- Replay should favor semantic locators with fallback selectors captured by the extension.
- The system should preserve original capture and partial artifacts even when replay fails.
- The system should optimize for a single local user, not CI or remote orchestration.
- The runtime may fail on highly dynamic apps, anti-bot protections, or flows that depend on unstable element identity.
- `v0.1.0` should make those failures inspectable rather than pretending to solve them completely.

## Testing Strategy

The release should include:

- schema validation tests for capture import
- unit tests for capture normalization
- smoke tests for replay on a stable sample app
- extension tests for scenario save/load and annotation persistence
- CLI smoke test for artifact generation

## Future Evolution

This design intentionally leaves room for later additions:

- prompt-generated plans as another input source
- richer assertions
- scenario editing
- cloud sync
- team sharing
- multi-browser capture

Those future modes should emit or consume the same normalized replay-plan contract where possible.
