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

test('createReplayPlan attaches annotations to their matching steps', () => {
  const plan = createReplayPlan({
    metadata: {
      schemaVersion: 1,
      scenarioId: 'annotated-scenario',
      baseUrl: 'https://example.com',
    },
    steps: [
      {
        type: 'navigate',
        url: 'https://example.com',
      },
      {
        type: 'click',
        selector: '#launch',
      },
    ],
    annotations: [
      {
        description: 'Open the flow',
        selector: '#launch',
        nearestStepId: 'step-2',
      },
      {
        description: 'Start here',
        selector: 'body',
        stepIndex: 0,
      },
    ],
    debug: {},
  });

  assert.deepEqual(plan, {
    version: 1,
    scenarioId: 'annotated-scenario',
    baseUrl: 'https://example.com',
    viewportPreset: 'desktop-1280',
    steps: [
      {
        id: 'step-1',
        type: 'navigate',
        url: 'https://example.com',
        annotations: [
          {
            description: 'Start here',
            selector: 'body',
            stepIndex: 0,
          },
        ],
      },
      {
        id: 'step-2',
        type: 'click',
        selector: '#launch',
        annotations: [
          {
            description: 'Open the flow',
            selector: '#launch',
            nearestStepId: 'step-2',
          },
        ],
      },
    ],
  });
});
