import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadCaptureBundle } from './capture-loader.js';
import { createReplayPlan } from './plan-generator.js';

test('createReplayPlan normalizes a capture bundle into the sample replay plan', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const capturePath = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');
  const planPath = path.resolve(testDir, '..', 'fixtures', 'sample-plan.json');

  const bundle = await loadCaptureBundle(capturePath);
  const plan = createReplayPlan(bundle);
  const expectedPlan = JSON.parse(await fs.readFile(planPath, 'utf8'));

  assert.deepEqual(plan, expectedPlan);
});

test('createReplayPlan preserves linked and unlinked annotations in stable places', () => {
  const plan = createReplayPlan({
    metadata: {
      schemaVersion: 1,
      scenarioId: 'annotated-scenario',
      baseUrl: 'https://example.com',
      viewportPreset: '',
    },
    steps: [
      {
        id: 'dup',
        type: 'navigate',
        url: 'https://example.com',
      },
      {
        id: 'dup',
        type: 'click',
        selector: '#launch',
      },
    ],
    annotations: [
      {
        description: 'Open the flow',
        selector: '#launch',
        nearestStepId: 'dup',
      },
      {
        description: 'Start here',
        selector: 'body',
        stepIndex: 0,
      },
      {
        description: 'Needs review',
        selector: '#orphan',
      },
    ],
    debug: {},
  });

  assert.deepEqual(plan, {
    version: 1,
    scenarioId: 'annotated-scenario',
    baseUrl: 'https://example.com',
    viewportPreset: 'desktop-1280',
    annotations: [
      {
        description: 'Open the flow',
        orderIndex: 0,
        selector: '#launch',
      },
      {
        description: 'Needs review',
        orderIndex: 2,
        selector: '#orphan',
      },
    ],
    steps: [
      {
        id: 'step-1',
        type: 'navigate',
        url: 'https://example.com',
        annotations: [
          {
            description: 'Start here',
            orderIndex: 1,
            selector: 'body',
          },
        ],
      },
      {
        id: 'step-2',
        type: 'click',
        selector: '#launch',
        annotations: [],
      },
    ],
  });
});

test('createReplayPlan preserves exact v2 viewport and timed pointer drags', () => {
  const plan = createReplayPlan({
    metadata: {
      schemaVersion: 2,
      scenarioId: 'rotate-model',
      viewportPreset: 'desktop-1280',
      viewport: { width: 1536, height: 864 },
    },
    steps: [
      {
        type: 'pointer_drag',
        selector: '#viewer',
        points: [
          { x: 100, y: 200, t: 0 },
          { x: 140, y: 220, t: 30 },
        ],
      },
    ],
    annotations: [],
    debug: {},
  });

  assert.deepEqual(plan.viewport, { width: 1536, height: 864 });
  assert.deepEqual(plan.steps[0], {
    id: 'step-1',
    type: 'pointer_drag',
    selector: '#viewer',
    points: [
      { x: 100, y: 200, t: 0 },
      { x: 140, y: 220, t: 30 },
    ],
    annotations: [],
  });
});

test('createReplayPlan preserves an optional fill typing delay', () => {
  const plan = createReplayPlan({
    metadata: { schemaVersion: 1, scenarioId: 'typed-prompt' },
    steps: [{ type: 'fill', selector: '#prompt', value: 'Create a plate', typingDelayMs: 12 }],
    annotations: [],
    debug: {},
  });
  assert.equal(plan.steps[0].typingDelayMs, 12);
});
