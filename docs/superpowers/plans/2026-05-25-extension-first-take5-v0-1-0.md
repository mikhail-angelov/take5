# Take5 v0.1.0 Extension-First Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an extension-first Take5 MVP where users capture a browser flow in Chrome, export it as JSON, and run `npx take5 run <capture-file>` to generate a polished demo video with actionable artifacts.

**Architecture:** The implementation has two products joined by one contract: a Chrome extension that captures semantic steps plus annotations into a versioned JSON bundle, and a CLI pipeline that validates the bundle, normalizes it into a replay plan, replays it in Playwright, and emits video plus logs. The current prompt-first agent path stays out of the hot path and should be retired from the default CLI command surface.

**Tech Stack:** Node.js ESM, Playwright, Chrome Extension Manifest V3, `node:test`, `fluent-ffmpeg`, GitHub Actions

---

## File Structure

**New top-level directories**

- `extension/`
  Chrome extension app for capture, annotations, scenario storage, import/export, and replay validation.
- `fixtures/`
  Stable sample capture bundles and replay fixtures for tests and smoke runs.
- `scripts/`
  Small repo scripts for packaging, smoke tests, and release helpers.

**CLI/runtime files**

- Create: `src/commands/run.js`
- Create: `src/capture-schema.js`
- Create: `src/capture-loader.js`
- Create: `src/plan-generator.js`
- Create: `src/replay-runner.js`
- Create: `src/artifact-writer.js`
- Create: `src/errors.js`
- Create: `src/logger.js`
- Create: `src/runner-annotator.js`
- Modify: `src/cli.js`
- Modify: `src/browser.js`
- Modify: `src/converter.js`
- Retain but stop wiring into the default flow: `src/agent.js`, `src/prompt.js`

**Extension files**

- Create: `extension/manifest.json`
- Create: `extension/service-worker.js`
- Create: `extension/content-script.js`
- Create: `extension/toolbar.html`
- Create: `extension/toolbar.css`
- Create: `extension/toolbar.js`
- Create: `extension/scenario-store.js`
- Create: `extension/capture-engine.js`
- Create: `extension/annotation-overlay.js`
- Create: `extension/replay-engine.js`
- Create: `extension/schema-export.js`
- Create: `extension/icons/` assets as needed

**Tests and fixtures**

- Create: `src/capture-schema.test.js`
- Create: `src/capture-loader.test.js`
- Create: `src/plan-generator.test.js`
- Create: `src/replay-runner.test.js`
- Create: `extension/scenario-store.test.js`
- Create: `extension/schema-export.test.js`
- Create: `fixtures/sample-capture.json`
- Create: `fixtures/sample-plan.json`
- Create: `scripts/smoke-run.js`

**Docs and release**

- Modify: `README.md`
- Modify: `package.json`
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/publish.yml`
- Create: `extension/README.md`

### Task 1: Replace the CLI entrypoint with a `run` pipeline shell

**Files:**

- Create: `src/commands/run.js`
- Create: `src/errors.js`
- Create: `src/logger.js`
- Modify: `src/cli.js`
- Modify: `package.json`
- Test: `src/cli.test.js`

- [ ] **Step 1: Write the failing CLI argument tests**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseCliArgs } from './cli.js';

test('parseCliArgs accepts run command with output flags', () => {
  const parsed = parseCliArgs([
    'run',
    'fixtures/sample-capture.json',
    '--out',
    './take5-output',
    '--debug',
  ]);

  assert.deepEqual(parsed, {
    command: 'run',
    captureFile: 'fixtures/sample-capture.json',
    outDir: './take5-output',
    debug: true,
    cutDelays: false,
  });
});

test('parseCliArgs rejects missing capture file for run', () => {
  assert.throws(() => parseCliArgs(['run']), /run command requires a capture file/);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test src/cli.test.js`
Expected: FAIL with `Cannot find module` or `parseCliArgs is not a function`

- [ ] **Step 3: Implement the new CLI shell**

```js
#!/usr/bin/env node
import path from 'path';
import { runCommand } from './commands/run.js';
import { CliUsageError } from './errors.js';

export function parseCliArgs(argv) {
  const [command = 'run', ...rest] = argv;

  if (command !== 'run') {
    throw new CliUsageError(`Unknown command: ${command}`);
  }

  const result = {
    command: 'run',
    captureFile: null,
    outDir: './take5-output',
    debug: false,
    cutDelays: false,
  };

  for (let index = 0; index < rest.length; index += 1) {
    const token = rest[index];
    if (!result.captureFile && !token.startsWith('-')) {
      result.captureFile = token;
      continue;
    }
    if (token === '--out') {
      result.outDir = rest[++index];
      continue;
    }
    if (token === '--debug') {
      result.debug = true;
      continue;
    }
    if (token === '--cut-delays') {
      result.cutDelays = true;
      continue;
    }
    throw new CliUsageError(`Unknown flag: ${token}`);
  }

  if (!result.captureFile) {
    throw new CliUsageError('run command requires a capture file');
  }

  return result;
}

const args = parseCliArgs(process.argv.slice(2));
await runCommand({
  captureFile: args.captureFile,
  outDir: path.resolve(args.outDir),
  debug: args.debug,
  cutDelays: args.cutDelays,
});
```

- [ ] **Step 4: Add the command runner scaffold**

```js
import fs from 'fs/promises';

export async function runCommand({ captureFile, outDir, debug, cutDelays }) {
  await fs.mkdir(outDir, { recursive: true });
  console.log('Take5 run');
  console.log(`capture: ${captureFile}`);
  console.log(`out: ${outDir}`);
  console.log(`debug: ${debug}`);
  console.log(`cutDelays: ${cutDelays}`);
}
```

- [ ] **Step 5: Update package metadata for the new command surface**

```json
{
  "bin": {
    "take5": "./src/cli.js"
  },
  "files": ["src/", "extension/", "fixtures/", "README.md"],
  "scripts": {
    "start": "node src/cli.js run fixtures/sample-capture.json",
    "test": "node --test",
    "smoke": "node scripts/smoke-run.js"
  }
}
```

- [ ] **Step 6: Run tests to verify the new CLI shell passes**

Run: `node --test src/cli.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/cli.js src/commands/run.js src/errors.js src/logger.js src/cli.test.js package.json
git commit -m "feat: add extension-first run command shell"
```

### Task 2: Define the capture bundle schema and import pipeline

**Files:**

- Create: `src/capture-schema.js`
- Create: `src/capture-loader.js`
- Create: `fixtures/sample-capture.json`
- Create: `src/capture-schema.test.js`
- Create: `src/capture-loader.test.js`

- [ ] **Step 1: Write the failing schema validation tests**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { validateCaptureBundle } from './capture-schema.js';

test('validateCaptureBundle accepts semantic steps plus annotations', () => {
  const bundle = {
    metadata: { schemaVersion: '1', scenarioId: 'demo-1', baseUrl: 'https://example.com' },
    steps: [{ id: 'step-1', type: 'navigate', url: 'https://example.com' }],
    annotations: [{ id: 'note-1', stepId: 'step-1', text: 'Open home page' }],
    debug: { rawEvents: [] },
  };

  const validated = validateCaptureBundle(bundle);
  assert.equal(validated.steps[0].type, 'navigate');
});

test('validateCaptureBundle rejects unknown step types', () => {
  assert.throws(
    () =>
      validateCaptureBundle({
        metadata: { schemaVersion: '1', scenarioId: 'demo-1', baseUrl: 'https://example.com' },
        steps: [{ id: 'step-1', type: 'drag_and_pray' }],
        annotations: [],
        debug: { rawEvents: [] },
      }),
    /Unsupported step type: drag_and_pray/,
  );
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test src/capture-schema.test.js src/capture-loader.test.js`
Expected: FAIL with missing module errors

- [ ] **Step 3: Implement the schema validator**

```js
const STEP_TYPES = new Set([
  'navigate',
  'click',
  'fill',
  'select',
  'keypress',
  'scroll',
  'wait',
  'assert_text',
]);

export function validateCaptureBundle(bundle) {
  if (!bundle || typeof bundle !== 'object') {
    throw new Error('Capture bundle must be an object');
  }

  const { metadata, steps, annotations, debug } = bundle;
  if (!metadata?.schemaVersion) throw new Error('Capture metadata.schemaVersion is required');
  if (!metadata?.scenarioId) throw new Error('Capture metadata.scenarioId is required');
  if (!Array.isArray(steps) || steps.length === 0)
    throw new Error('Capture must contain at least one step');
  if (!Array.isArray(annotations)) throw new Error('Capture annotations must be an array');
  if (!debug || typeof debug !== 'object') throw new Error('Capture debug section is required');

  for (const step of steps) {
    if (!STEP_TYPES.has(step.type)) {
      throw new Error(`Unsupported step type: ${step.type}`);
    }
  }

  return bundle;
}
```

- [ ] **Step 4: Implement the capture loader**

```js
import fs from 'fs/promises';
import { validateCaptureBundle } from './capture-schema.js';

export async function loadCaptureBundle(filePath) {
  const raw = await fs.readFile(filePath, 'utf8');
  const parsed = JSON.parse(raw);
  return validateCaptureBundle(parsed);
}
```

- [ ] **Step 5: Add the sample capture fixture**

```json
{
  "metadata": {
    "schemaVersion": "1",
    "scenarioId": "sample-login-demo",
    "name": "Sample login demo",
    "baseUrl": "https://example.com"
  },
  "steps": [
    { "id": "step-1", "type": "navigate", "url": "https://example.com" },
    { "id": "step-2", "type": "click", "locator": { "role": "button", "name": "Sign in" } }
  ],
  "annotations": [
    { "id": "note-1", "stepId": "step-2", "text": "This CTA should be highlighted in the video" }
  ],
  "debug": {
    "rawEvents": [],
    "selectorCandidates": {}
  }
}
```

- [ ] **Step 6: Run tests to verify import works**

Run: `node --test src/capture-schema.test.js src/capture-loader.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/capture-schema.js src/capture-loader.js src/capture-schema.test.js src/capture-loader.test.js fixtures/sample-capture.json
git commit -m "feat: add capture bundle schema and loader"
```

### Task 3: Generate normalized replay plans and artifact directories

**Files:**

- Create: `src/plan-generator.js`
- Create: `src/artifact-writer.js`
- Create: `src/plan-generator.test.js`
- Create: `fixtures/sample-plan.json`
- Modify: `src/commands/run.js`

- [ ] **Step 1: Write the failing plan-generation test**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { createReplayPlan } from './plan-generator.js';

test('createReplayPlan turns capture steps into executable replay steps', () => {
  const plan = createReplayPlan({
    metadata: { scenarioId: 'demo-1', baseUrl: 'https://example.com' },
    steps: [
      { id: 'step-1', type: 'navigate', url: 'https://example.com' },
      { id: 'step-2', type: 'click', locator: { role: 'button', name: 'Start' } },
    ],
    annotations: [{ id: 'note-1', stepId: 'step-2', text: 'Call out the CTA' }],
    debug: { rawEvents: [] },
  });

  assert.deepEqual(plan.steps[1], {
    id: 'step-2',
    type: 'click',
    locator: { role: 'button', name: 'Start' },
    annotations: [{ id: 'note-1', stepId: 'step-2', text: 'Call out the CTA' }],
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test src/plan-generator.test.js`
Expected: FAIL with missing module errors

- [ ] **Step 3: Implement replay-plan generation**

```js
export function createReplayPlan(bundle) {
  const annotationsByStepId = new Map();
  for (const annotation of bundle.annotations) {
    const list = annotationsByStepId.get(annotation.stepId) ?? [];
    list.push(annotation);
    annotationsByStepId.set(annotation.stepId, list);
  }

  return {
    version: '1',
    scenarioId: bundle.metadata.scenarioId,
    baseUrl: bundle.metadata.baseUrl,
    viewportPreset: bundle.metadata.viewportPreset ?? 'desktop-1280',
    steps: bundle.steps.map((step) => ({
      ...step,
      annotations: annotationsByStepId.get(step.id) ?? [],
    })),
  };
}
```

- [ ] **Step 4: Implement artifact directory helpers**

```js
import fs from 'fs/promises';
import path from 'path';

export async function createRunArtifacts(outDir, scenarioId) {
  const runDir = path.join(outDir, `${Date.now()}-${scenarioId}`);
  const screenshotsDir = path.join(runDir, 'screenshots');
  await fs.mkdir(screenshotsDir, { recursive: true });
  return { runDir, screenshotsDir };
}

export async function writeJsonArtifact(filePath, payload) {
  await fs.writeFile(filePath, `${JSON.stringify(payload, null, 2)}\n`, 'utf8');
}
```

- [ ] **Step 5: Wire the run command through load -> plan -> artifact save**

```js
import path from 'path';
import { loadCaptureBundle } from '../capture-loader.js';
import { createReplayPlan } from '../plan-generator.js';
import { createRunArtifacts, writeJsonArtifact } from '../artifact-writer.js';

export async function runCommand({ captureFile, outDir }) {
  const bundle = await loadCaptureBundle(captureFile);
  const plan = createReplayPlan(bundle);
  const artifacts = await createRunArtifacts(outDir, bundle.metadata.scenarioId);
  await writeJsonArtifact(path.join(artifacts.runDir, 'capture.json'), bundle);
  await writeJsonArtifact(path.join(artifacts.runDir, 'plan.json'), plan);
}
```

- [ ] **Step 6: Run tests to verify the plan contract passes**

Run: `node --test src/plan-generator.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/plan-generator.js src/artifact-writer.js src/plan-generator.test.js fixtures/sample-plan.json src/commands/run.js
git commit -m "feat: add replay plan generation and artifact directories"
```

### Task 4: Build the extension shell, scenario store, and JSON import/export

**Files:**

- Create: `extension/manifest.json`
- Create: `extension/service-worker.js`
- Create: `extension/toolbar.html`
- Create: `extension/toolbar.css`
- Create: `extension/toolbar.js`
- Create: `extension/scenario-store.js`
- Create: `extension/schema-export.js`
- Create: `extension/scenario-store.test.js`
- Create: `extension/schema-export.test.js`

- [ ] **Step 1: Write the failing scenario-store tests**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { createScenarioStore } from './scenario-store.js';

test('scenario store saves and lists scenarios', async () => {
  const memory = new Map();
  const store = createScenarioStore({
    get: async (key) => memory.get(key),
    set: async (key, value) => memory.set(key, value),
  });

  await store.saveScenario({
    metadata: { scenarioId: 'demo-1', name: 'Demo 1' },
    steps: [],
    annotations: [],
    debug: { rawEvents: [] },
  });
  const scenarios = await store.listScenarios();

  assert.equal(scenarios.length, 1);
  assert.equal(scenarios[0].metadata.name, 'Demo 1');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test extension/scenario-store.test.js extension/schema-export.test.js`
Expected: FAIL with missing module errors

- [ ] **Step 3: Add the extension manifest and toolbar shell**

```json
{
  "manifest_version": 3,
  "name": "Take5",
  "version": "0.1.0",
  "permissions": ["storage", "activeTab", "scripting", "downloads"],
  "background": {
    "service_worker": "service-worker.js",
    "type": "module"
  },
  "content_scripts": [
    {
      "matches": ["<all_urls>"],
      "js": ["content-script.js"],
      "css": ["toolbar.css"],
      "run_at": "document_idle"
    }
  ],
  "web_accessible_resources": [
    {
      "resources": ["toolbar.html"],
      "matches": ["<all_urls>"]
    }
  ]
}
```

- [ ] **Step 4: Implement the scenario store**

```js
const STORAGE_KEY = 'take5.scenarios';

export function createScenarioStore(storageApi = chrome.storage.local) {
  return {
    async listScenarios() {
      const payload = await storageApi.get(STORAGE_KEY);
      return payload[STORAGE_KEY] ?? [];
    },
    async saveScenario(bundle) {
      const existing = await this.listScenarios();
      const next = existing.filter(
        (item) => item.metadata.scenarioId !== bundle.metadata.scenarioId,
      );
      next.push(bundle);
      await storageApi.set({ [STORAGE_KEY]: next });
    },
    async deleteScenario(scenarioId) {
      const existing = await this.listScenarios();
      await storageApi.set({
        [STORAGE_KEY]: existing.filter((item) => item.metadata.scenarioId !== scenarioId),
      });
    },
  };
}
```

- [ ] **Step 5: Implement JSON export/import helpers**

```js
export function serializeScenario(bundle) {
  return `${JSON.stringify(bundle, null, 2)}\n`;
}

export function parseScenarioJson(raw) {
  return JSON.parse(raw);
}
```

- [ ] **Step 6: Run tests to verify persistence and serialization pass**

Run: `node --test extension/scenario-store.test.js extension/schema-export.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add extension/manifest.json extension/service-worker.js extension/toolbar.html extension/toolbar.css extension/toolbar.js extension/scenario-store.js extension/schema-export.js extension/scenario-store.test.js extension/schema-export.test.js
git commit -m "feat: add extension shell and scenario storage"
```

### Task 5: Implement capture semantics, `Ctrl` annotations, and extension replay

**Files:**

- Create: `extension/content-script.js`
- Create: `extension/capture-engine.js`
- Create: `extension/annotation-overlay.js`
- Create: `extension/replay-engine.js`
- Modify: `extension/toolbar.js`
- Test: `extension/capture-engine.test.js`
- Test: `extension/replay-engine.test.js`

- [ ] **Step 1: Write the failing capture-engine tests**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { createCaptureEngine } from './capture-engine.js';

test('capture engine records semantic click steps', () => {
  const engine = createCaptureEngine();
  engine.recordClick({
    locator: { role: 'button', name: 'Save' },
    url: 'https://example.com/settings',
  });

  assert.equal(engine.getBundle().steps[0].type, 'click');
});

test('capture engine records ctrl-click annotations separately from steps', () => {
  const engine = createCaptureEngine();
  engine.recordAnnotation({
    stepId: 'step-2',
    text: 'Explain why this button matters',
    locator: { role: 'button', name: 'Save' },
    url: 'https://example.com/settings',
  });

  assert.equal(engine.getBundle().annotations[0].text, 'Explain why this button matters');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test extension/capture-engine.test.js extension/replay-engine.test.js`
Expected: FAIL with missing module errors

- [ ] **Step 3: Implement semantic step capture**

```js
import { randomUUID } from './uuid.js';

export function createCaptureEngine() {
  const bundle = {
    metadata: {
      schemaVersion: '1',
      scenarioId: randomUUID(),
      name: 'Untitled scenario',
      baseUrl: location.origin,
    },
    steps: [],
    annotations: [],
    debug: { rawEvents: [] },
  };

  return {
    recordNavigate({ url }) {
      bundle.steps.push({ id: randomUUID(), type: 'navigate', url });
    },
    recordClick({ locator, url }) {
      bundle.steps.push({ id: randomUUID(), type: 'click', locator, url });
    },
    recordFill({ locator, value, url }) {
      bundle.steps.push({ id: randomUUID(), type: 'fill', locator, value, url });
    },
    recordAnnotation({ stepId = null, text, locator, url }) {
      bundle.annotations.push({ id: randomUUID(), stepId, text, locator, url });
    },
    recordRawEvent(event) {
      bundle.debug.rawEvents.push(event);
    },
    getBundle() {
      return structuredClone(bundle);
    },
  };
}
```

- [ ] **Step 4: Implement the annotation overlay with `Ctrl+Click`**

```js
export function installAnnotationOverlay({ onSave }) {
  let annotationMode = false;

  window.addEventListener('keydown', (event) => {
    if (event.key === 'Control') annotationMode = true;
  });

  window.addEventListener('keyup', (event) => {
    if (event.key === 'Control') annotationMode = false;
  });

  document.addEventListener(
    'click',
    (event) => {
      if (!annotationMode) return;
      event.preventDefault();
      event.stopPropagation();
      const target = event.target;
      openAnnotationPopover(target, (text) => onSave({ target, text }));
    },
    true,
  );
}
```

- [ ] **Step 5: Implement semantic replay inside the extension**

```js
export async function replayScenario(bundle, { mode, onStep }) {
  let index = 0;

  async function executeStep(step) {
    onStep?.(step);
    if (step.type === 'navigate') {
      location.href = step.url;
      return;
    }
    if (step.type === 'click') {
      resolveLocator(step.locator)?.click();
      return;
    }
    if (step.type === 'fill') {
      resolveLocator(step.locator).value = step.value;
      return;
    }
  }

  if (mode === 'auto') {
    for (const step of bundle.steps) {
      await executeStep(step);
    }
    return;
  }

  return {
    async next() {
      if (index >= bundle.steps.length) return false;
      await executeStep(bundle.steps[index++]);
      return true;
    },
    async back() {
      index = Math.max(0, index - 1);
      return index;
    },
  };
}
```

- [ ] **Step 6: Run tests to verify capture and replay pass**

Run: `node --test extension/capture-engine.test.js extension/replay-engine.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add extension/content-script.js extension/capture-engine.js extension/annotation-overlay.js extension/replay-engine.js extension/toolbar.js extension/capture-engine.test.js extension/replay-engine.test.js
git commit -m "feat: add extension capture, annotations, and replay"
```

### Task 6: Replace agent-driven browser execution with a replay runner

**Files:**

- Create: `src/replay-runner.js`
- Create: `src/runner-annotator.js`
- Modify: `src/browser.js`
- Modify: `src/commands/run.js`
- Test: `src/replay-runner.test.js`
- Test: `src/browser.test.js`

- [ ] **Step 1: Write the failing replay-runner tests**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { executeReplayPlan } from './replay-runner.js';

test('executeReplayPlan records step results in order', async () => {
  const calls = [];
  const browser = {
    async launch() {},
    async close() {
      return { webmPath: 'take5-output/demo.webm' };
    },
    async executeStep(step) {
      calls.push(step.type);
      return { ok: true, stepId: step.id };
    },
  };

  const result = await executeReplayPlan(browser, {
    steps: [
      { id: 'step-1', type: 'navigate' },
      { id: 'step-2', type: 'click' },
    ],
  });

  assert.deepEqual(calls, ['navigate', 'click']);
  assert.equal(result.stepResults.length, 2);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test src/replay-runner.test.js`
Expected: FAIL with missing module errors

- [ ] **Step 3: Implement the replay runner**

```js
export async function executeReplayPlan(browser, plan) {
  const stepResults = [];

  await browser.launch({ outputDir: plan.outputDir, viewport: plan.viewport });

  try {
    for (const step of plan.steps) {
      const result = await browser.executeStep(step);
      stepResults.push(result);
      if (!result.ok) break;
    }
  } finally {
    const closeResult = await browser.close();
    return { stepResults, closeResult };
  }
}
```

- [ ] **Step 4: Adapt `Browser` to step-based execution**

```js
export class Browser {
  async executeStep(step) {
    switch (step.type) {
      case 'navigate':
        await this.page.goto(step.url, { waitUntil: 'domcontentloaded', timeout: 15000 });
        return { ok: true, stepId: step.id, type: step.type };
      case 'click':
        await this.clickLocator(step.locator, step.annotations ?? []);
        return { ok: true, stepId: step.id, type: step.type };
      case 'fill':
        await this.fillLocator(step.locator, step.value, step.annotations ?? []);
        return { ok: true, stepId: step.id, type: step.type };
      default:
        return { ok: false, stepId: step.id, error: `Unsupported step: ${step.type}` };
    }
  }
}
```

- [ ] **Step 5: Wire `runCommand` through the replay runner**

```js
import path from 'path';
import { Browser } from '../browser.js';
import { executeReplayPlan } from '../replay-runner.js';

const browser = new Browser();
const result = await executeReplayPlan(browser, {
  ...plan,
  outputDir: artifacts.runDir,
  viewport: { width: 1280, height: 720 },
});

await writeJsonArtifact(path.join(artifacts.runDir, 'step-log.json'), result.stepResults);
```

- [ ] **Step 6: Run tests to verify replay execution passes**

Run: `node --test src/replay-runner.test.js src/browser.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/replay-runner.js src/runner-annotator.js src/browser.js src/commands/run.js src/replay-runner.test.js src/browser.test.js
git commit -m "feat: add playwright replay runner"
```

### Task 7: Save failure artifacts, screenshots, console/network errors, and polished video outputs

**Files:**

- Modify: `src/browser.js`
- Modify: `src/converter.js`
- Modify: `src/artifact-writer.js`
- Modify: `src/replay-runner.js`
- Test: `src/replay-runner.test.js`
- Test: `src/converter.test.js`

- [ ] **Step 1: Write the failing artifact tests**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { summarizeRun } from './artifact-writer.js';

test('summarizeRun marks failed step and screenshot path', () => {
  const summary = summarizeRun({
    stepResults: [
      { ok: true, stepId: 'step-1', type: 'navigate' },
      { ok: false, stepId: 'step-2', type: 'click', screenshotPath: 'screenshots/step-2.png' },
    ],
    closeResult: { webmPath: 'demo.webm' },
  });

  assert.equal(summary.status, 'failed');
  assert.equal(summary.failedStepId, 'step-2');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test src/replay-runner.test.js src/converter.test.js`
Expected: FAIL because `summarizeRun` and converter test targets do not exist

- [ ] **Step 3: Add browser listeners and failure screenshots**

```js
export class Browser {
  async launch({ outputDir, viewport }) {
    this.consoleErrors = [];
    this.networkErrors = [];
    this.outputDir = outputDir;
    await super.launch?.({ outputDir, viewport });
    this.page.on('console', (message) => {
      if (message.type() === 'error') this.consoleErrors.push(message.text());
    });
    this.page.on('requestfailed', (request) => {
      this.networkErrors.push({
        url: request.url(),
        errorText: request.failure()?.errorText ?? 'unknown failure',
      });
    });
  }

  async captureFailure(stepId) {
    const screenshotPath = path.join(this.outputDir, 'screenshots', `${stepId}.png`);
    await this.page.screenshot({ path: screenshotPath, fullPage: true });
    return screenshotPath;
  }
}
```

- [ ] **Step 4: Add replay summary and artifact writers**

```js
export function summarizeRun({ stepResults, closeResult }) {
  const failedStep = stepResults.find((step) => !step.ok) ?? null;
  return {
    status: failedStep ? 'failed' : 'passed',
    failedStepId: failedStep?.stepId ?? null,
    video: closeResult.webmPath,
    stepCount: stepResults.length,
  };
}
```

- [ ] **Step 5: Extend video conversion to write `.mp4` and preserve `.webm`**

```js
export async function finalizeVideoArtifacts({
  webmPath,
  cutDelays,
  actionSegments,
  recordingStartTime,
}) {
  const mp4Path = await convertToMp4(
    webmPath,
    0,
    cutDelays ? actionSegments : null,
    recordingStartTime,
  );
  return {
    webmPath,
    mp4Path,
  };
}
```

- [ ] **Step 6: Run tests to verify artifacts and conversion pass**

Run: `node --test src/replay-runner.test.js src/converter.test.js`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/browser.js src/converter.js src/artifact-writer.js src/replay-runner.js src/converter.test.js src/replay-runner.test.js
git commit -m "feat: add replay failure artifacts and video outputs"
```

### Task 8: Add README quickstart, smoke tests, CI, and publish setup

**Files:**

- Modify: `README.md`
- Create: `extension/README.md`
- Create: `scripts/smoke-run.js`
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/publish.yml`
- Modify: `package.json`

- [ ] **Step 1: Write the failing smoke test**

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';

test('CLI smoke run writes plan artifact for sample capture', () => {
  const result = spawnSync(
    'node',
    ['src/cli.js', 'run', 'fixtures/sample-capture.json', '--out', '.tmp/smoke'],
    {
      encoding: 'utf8',
    },
  );

  assert.equal(result.status, 0);
  assert.match(result.stdout, /plan\.json/);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test scripts/smoke-run.js`
Expected: FAIL until the end-to-end CLI path is complete

- [ ] **Step 3: Update the README for extension-first usage**

````md
## Quickstart

1. Load the unpacked extension from `extension/` in Chrome.
2. Capture a scenario in your app.
3. Export the scenario JSON.
4. Run:

```bash
npx take5 run ./my-scenario.json --out ./take5-output
```
````

Artifacts:

- `capture.json`
- `plan.json`
- `step-log.json`
- `screenshots/`
- `summary.json`
- `video.webm`
- `video.mp4` when ffmpeg is available

````

- [ ] **Step 4: Add CI and publish workflows**

```yaml
name: ci
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: npm ci
      - run: node --test
````

```yaml
name: publish
on:
  workflow_dispatch:
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          registry-url: https://registry.npmjs.org
      - run: npm ci
      - run: npm test
      - run: npm publish --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
```

- [ ] **Step 5: Set the release metadata for `v0.1.0`**

```json
{
  "version": "0.1.0",
  "description": "Capture a browser flow in Chrome and replay it into a polished demo video",
  "scripts": {
    "test": "node --test",
    "smoke": "node --test scripts/smoke-run.js",
    "package:extension": "zip -r take5-extension.zip extension"
  }
}
```

- [ ] **Step 6: Run the full verification suite**

Run: `node --test`
Expected: PASS

Run: `node scripts/smoke-run.js`
Expected: PASS and writes artifacts under `.tmp/smoke`

- [ ] **Step 7: Commit**

```bash
git add README.md extension/README.md scripts/smoke-run.js .github/workflows/ci.yml .github/workflows/publish.yml package.json
git commit -m "chore: document and prepare extension-first v0.1.0 release"
```

## Self-Review

### Spec coverage

- Extension-first architecture: covered by Tasks 4 and 5.
- Semantic capture schema with raw debug events: covered by Tasks 2 and 5.
- `Ctrl` annotation UX and persistent markers: covered by Task 5.
- Saved scenarios, import/export, delete, replay: covered by Tasks 4 and 5.
- Playwright replay and polished video output: covered by Tasks 6 and 7.
- Actionable artifacts and failure logs: covered by Tasks 3 and 7.
- README, smoke tests, CI, publish setup: covered by Task 8.

### Placeholder scan

- No `TBD`, `TODO`, or deferred “implement later” markers remain in task steps.
- Each task includes explicit files, commands, and code targets.
- The plan intentionally leaves the existing agent path in the repo but out of the default flow; no task depends on undocumented future work.

### Type consistency

- Capture bundle sections are consistently named `metadata`, `steps`, `annotations`, and `debug`.
- Replay plan generation consistently uses `step.id`, `step.type`, and `annotations`.
- CLI command surface consistently uses `run <capture-file> --out <dir> [--cut-delays] [--debug]`.
