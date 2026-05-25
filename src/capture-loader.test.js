import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadCaptureBundle } from './capture-loader.js';

test('loadCaptureBundle reads and validates the sample capture fixture', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const capturePath = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');

  const bundle = await loadCaptureBundle(capturePath);

  assert.equal(bundle.metadata.schemaVersion, 1);
  assert.equal(bundle.metadata.scenarioId, 'sample-scenario');
  assert.deepEqual(bundle.steps, [
    {
      type: 'navigate',
      url: 'https://example.com',
    },
  ]);
  assert.deepEqual(bundle.annotations, []);
  assert.deepEqual(bundle.debug, {});
});

test('loadCaptureBundle rejects invalid JSON payloads through validation', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const capturePath = path.resolve(testDir, '..', 'take5-output-invalid-capture.json');

  await fs.writeFile(
    capturePath,
    JSON.stringify({
      metadata: {
        schemaVersion: 1,
        scenarioId: 'invalid-capture',
      },
      steps: [{ type: 'drag_and_pray' }],
      annotations: [],
      debug: {},
    }),
  );

  try {
    await assert.rejects(loadCaptureBundle(capturePath), /Unsupported step type: drag_and_pray/);
  } finally {
    await fs.unlink(capturePath);
  }
});

