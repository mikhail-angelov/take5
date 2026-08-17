# @mikhail.angelov/take5

Take5 is a local-first, **extension-first** tool for turning a manually captured
web-app flow into a polished, reproducible demo — without authoring selectors or
scripts.

- A **Chrome extension** records the semantic steps of a flow while you click
  through your app, lets you attach inline annotations, and exports the capture
  as JSON.
- A **CLI** ingests that capture, normalizes it into a deterministic replay plan,
  and (with `--render`) replays it in Playwright and renders a polished demo video
  via [`playwright-recast`](https://github.com/ThePatriczek/playwright-recast).

> **Status:** capture → plan → artifacts works. With `--render`, the CLI replays
> the plan in headless Chromium and produces `demo.mp4` with a synthesized gliding
> cursor, click ripples, auto-zoom, idle-trimming, and positional callouts from
> annotations. Narrated subtitles / AI captions are the next slice — see
> `docs/2026-08-01-cleanup-and-direction-spec.md`.

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
take5 run <capture-file.json> [--out <dir>] [--render] [--ai-captions] [--cut-delays] [--debug]
```

Artifacts only (fast, no browser):

```bash
take5 run fixtures/sample-capture.json --out ./take5-output
```

Full render to video:

```bash
take5 run my-capture.json --out ./take5-output --render
```

The run writes a timestamped directory. Without `--render`:

```text
take5-output/<run-id>/
├── capture.json   # the validated capture bundle
└── plan.json      # the normalized replay plan
```

With `--render` it additionally records and renders:

```text
├── trace.zip      # Playwright trace of the replay
├── video.webm     # raw browser recording
└── demo.mp4       # polished demo: cursor, zoom, click ripples, captions, trimmed idle
```

### Flags

- `--out <dir>` — output directory (default `./take5-output`)
- `--render` — replay in headless Chromium and render `demo.mp4`
- `--ai-captions` — rewrite step captions into natural narration with an LLM
- `--cut-delays` — trim idle stretches more aggressively during render
- `--user-data-dir <dir>` — replay in a persistent browser profile (see below)
- `--auth-url <url>` — visit this URL before the plan to establish a session
- `--pat <token>` — headless auth via a Personal Access Token (see below); prefer
  the `TAKE5_PAT` env var so the secret stays out of argv / shell history
- `--debug` — verbose logging, including stack-trace details on failure

Burned-in narration captions are generated from the plan steps automatically
during `--render`. `--ai-captions` upgrades them via an OpenAI-compatible LLM,
configured through the environment (falls back to the deterministic captions if
unset or unavailable):

```bash
export TAKE5_LLM_API_KEY=sk-...            # required for --ai-captions
export TAKE5_LLM_BASE_URL=https://api.openai.com/v1   # optional
export TAKE5_LLM_MODEL=gpt-4o-mini                    # optional
```

## Authenticated apps

`--render` launches its **own** headless browser (Playwright), separate from your
everyday Chrome — so your normal browser's login does not carry over, and macOS
app-bound cookie encryption prevents copying cookies out of your Chrome profile.
There are two ways to give the replay an authenticated session.

### One-off: magic link / callback URL

If your app can produce a login URL that sets the session on visit (a magic link,
SSO callback, `?token=…`), pass it with `--auth-url`. Take5 visits it before the
plan runs, so the session cookie is set for the rest of the replay:

```bash
take5 run flow.json --render \
  --auth-url "https://app.example.com/api/auth/callback?token=…"
```

The URL is a runtime-only argument: it is never written to the capture, the plan,
the artifacts, or the burned-in captions. Magic links are usually short-lived and
single-use, so each run needs a fresh one — unless you persist the session:

### Reusable: a dedicated Take5 profile

Point `--user-data-dir` at a **dedicated** directory (not your real Chrome
profile). Seed it once with `--auth-url`; the session cookie is saved into that
profile and reused on later runs with no magic link, until the session expires
server-side:

```bash
# 1. Seed the profile once (a real Chrome window opens; approve any Keychain
#    prompt so cookies can be stored). Requires the 'chrome' channel:
#    npx playwright install chrome
take5 run flow.json --render \
  --user-data-dir ~/.take5-chrome \
  --auth-url "https://app.example.com/api/auth/callback?token=…"

# 2. Reuse the saved session — no magic link needed:
take5 run flow.json --render --user-data-dir ~/.take5-chrome
```

Notes:

- With `--user-data-dir`, replay runs headed (a visible window) so a macOS
  Keychain prompt to encrypt/decrypt the profile's cookies can be approved.
- The directory must not be in use by another running browser at the same time.

### Headless: a Personal Access Token (PAT)

If the target app exposes a PAT bootstrap that seeds a token into the origin's
`sessionStorage` before boot (the "SPEC22" contract — a human mints the token
once, the agent consumes it), pass it with `--pat`. Take5 runs in a fresh,
isolated context, seeds the token via a document-start init script (guarded to
the top frame and exact app origin), and lets the app exchange it for a session
cookie — no magic link, no email, fully headless:

```bash
# Prefer the env var so the token never lands in argv / shell history:
export TAKE5_PAT=pat_…
take5 run flow.json --render
```

The app origin is derived from the plan's first `navigate` step. If the exchange
is rejected (the app sets `data-auth-error` on its root), the run fails fast
before the rest of the plan proceeds on the wrong identity. `--pat` cannot be
combined with `--user-data-dir` — the PAT contract requires a clean context, and
a persistent profile would break identity isolation.

## Requirements

- Node.js 20.6+
- Chrome, for the capture extension

For `--render`:

- Playwright Chromium — `npx playwright install chromium`
- `ffmpeg` and `ffprobe` on the `PATH`
- For `--user-data-dir` (persistent profile), the real Chrome build —
  `npx playwright install chrome`

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
