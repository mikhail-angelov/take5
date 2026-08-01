# @mikhail.angelov/take5

Take5 is a local-first, **extension-first** tool for turning a manually captured
web-app flow into a polished, reproducible demo — without authoring selectors or
scripts.

- A **Chrome extension** records the semantic steps of a flow while you click
  through your app, lets you attach inline annotations, and exports the capture
  as JSON.
- A **CLI** ingests that capture, normalizes it into a deterministic replay plan,
  and (on the roadmap) replays it in Playwright to render an annotated demo video.

> **Status:** the CLI currently ingests a capture and produces replay artifacts
> (`capture.json` + `plan.json`). The Playwright replay and video render are the
> next phase — see `docs/2026-08-01-cleanup-and-direction-spec.md`.

## Installation

```bash
npm install -g @mikhail.angelov/take5
```

Or locally:

```bash
npm install @mikhail.angelov/take5
```

## Capture (Chrome extension)

1. Load the unpacked extension from `extension/` (`chrome://extensions` →
   Developer mode → Load unpacked).
2. Open your web app and start a capture from the Take5 toolbar.
3. Perform the flow. Hold `Ctrl` and click an element to attach an inline
   annotation.
4. Stop capture, then export the scenario to a JSON file.

## Run (CLI)

```bash
take5 run <capture-file.json> [--out <dir>] [--cut-delays] [--debug]
```

Example:

```bash
take5 run fixtures/sample-capture.json --out ./take5-output
```

This validates the capture, normalizes it into a replay plan, and writes a
timestamped run directory containing:

```text
take5-output/<run-id>/
├── capture.json   # the validated capture bundle
└── plan.json      # the normalized replay plan
```

### Flags

- `--out <dir>` — output directory (default `./take5-output`)
- `--cut-delays` — mark idle gaps for trimming in the (upcoming) render phase
- `--debug` — verbose logging, including stack-trace details on failure

## Requirements

- Node.js 20.6+
- Chrome, for the capture extension

The upcoming render phase will additionally require Playwright Chromium and
`ffmpeg`/`ffprobe` on the `PATH`.

## Testing

```bash
npm test
```

Native `node --test` suite covering the CLI, capture schema validation, replay
plan normalization, artifact writing, and the extension modules.

## Architecture

```text
take5/
├── extension/            # Chrome extension: capture, annotate, replay-preview
│   ├── capture-schema.js  # ← single source of truth for the data contract
│   └── plan-generator.js  # ← single source of truth for plan normalization
├── src/                  # CLI
│   ├── cli.js             # entry point + arg parsing
│   ├── commands/run.js    # capture → plan → artifacts
│   ├── capture-loader.js
│   ├── capture-schema.js  # re-exports the extension contract (no duplication)
│   ├── plan-generator.js  # re-exports the extension contract (no duplication)
│   └── artifact-writer.js
└── docs/
    ├── prd.md
    └── 2026-08-01-cleanup-and-direction-spec.md   # direction + target architecture
```

The capture schema and plan generator live once in `extension/` (so the browser
extension package is self-contained) and are re-exported by `src/` for the CLI.
