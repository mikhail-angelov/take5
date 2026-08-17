# Take5 Extension

Chrome (Manifest V3) extension that captures a web-app flow as semantic steps,
lets you attach inline annotations, previews replay, and exports the capture as
JSON for the `take5` CLI.

## Load (unpacked)

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. **Load unpacked** → select this `extension/` directory.
4. Open a target app. The toolbar stays hidden on every page until you click the
   extension action, which toggles it for that tab (the choice survives
   navigations inside the tab).

## Capture

- Start a capture from the toolbar, then perform your flow.
- Recorded steps: navigate, click, fill, select, keypress, scroll, wait,
  assert_text.
- Hold `Ctrl` and click an element to attach an inline annotation.
- Stop, save to extension storage, and optionally export to JSON.

## Module map

- `service-worker.js` — background: scenario storage messaging, toolbar toggle.
- `content-script.js` — classic (non-module) script injected into pages; boots the toolbar/overlay. Kept classic on purpose (see `content-script-syntax.test.js`).
- `capture-engine.js` — turns DOM events into semantic capture steps.
- `annotation-overlay.js` — in-page annotation overlay UI.
- `replay-engine.js` — in-extension replay preview.
- `scenario-store.js` — persistence over `chrome.storage`.
- `toolbar-visibility-state.js` — background: per-tab "is the panel shown" flag.
- `toolbar-visibility.js` — in-page show/hide of the toolbar frame.
- `schema-export.js` — serialize/parse/validate capture JSON and export filename.
- `capture-schema.js` — **single source of truth** for the capture data contract (the CLI re-exports it).
- `plan-generator.js` — **single source of truth** for replay-plan normalization (the CLI re-exports it).

## Data contract

`capture-schema.js` and `plan-generator.js` live here (not in `src/`) so the
extension package is self-contained in the browser. The CLI re-exports both from
`src/` — edit them here, never duplicate.

## Tests

Run from the repo root:

```bash
npm test
```
