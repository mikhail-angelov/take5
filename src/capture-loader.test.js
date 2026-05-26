import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
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
  const captureDir = await fs.mkdtemp(path.join(os.tmpdir(), 'take5-capture-'));
  const capturePath = path.join(captureDir, `invalid-${randomUUID()}.json`);

  await fs.writeFile(capturePath, JSON.stringify({
    metadata: {
      schemaVersion: 1,
      scenarioId: 'invalid-capture',
    },
    steps: [{ type: 'drag_and_pray' }],
    annotations: [],
    debug: {},
  }));

  try {
    await assert.rejects(loadCaptureBundle(capturePath), /Unsupported step type: drag_and_pray/);
  } finally {
    await fs.rm(captureDir, { recursive: true, force: true });
  }
});

test('loadCaptureBundle rejects malformed JSON', async () => {
  const captureDir = await fs.mkdtemp(path.join(os.tmpdir(), 'take5-capture-'));
  const capturePath = path.join(captureDir, `malformed-${randomUUID()}.json`);

  await fs.writeFile(capturePath, '{"metadata":');

  try {
    await assert.rejects(loadCaptureBundle(capturePath), SyntaxError);
  } finally {
    await fs.rm(captureDir, { recursive: true, force: true });
  }
});

test('loadCaptureBundle accepts BOM-prefixed JSON', async () => {
  const captureDir = await fs.mkdtemp(path.join(os.tmpdir(), 'take5-capture-'));
  const capturePath = path.join(captureDir, `bom-${randomUUID()}.json`);

  await fs.writeFile(
    capturePath,
    '\uFEFF' +
      JSON.stringify({
        metadata: {
          schemaVersion: 1,
          scenarioId: 'bom-capture',
        },
        steps: [
          {
            type: 'navigate',
            url: 'https://example.com',
          },
        ],
        annotations: [],
        debug: {},
      }),
  );

  try {
    const bundle = await loadCaptureBundle(capturePath);
    assert.equal(bundle.metadata.scenarioId, 'bom-capture');
  } finally {
    await fs.rm(captureDir, { recursive: true, force: true });
  }
});
