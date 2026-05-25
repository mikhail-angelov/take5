import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createRunDirectory } from './artifact-writer.js';

test('createRunDirectory yields distinct paths for repeated same-scenario calls', async () => {
  const outDir = await fs.mkdtemp(path.join(os.tmpdir(), 'take5-artifacts-'));

  try {
    const first = await createRunDirectory(outDir, 'same-scenario');
    const second = await createRunDirectory(outDir, 'same-scenario');

    assert.notEqual(first.runDir, second.runDir);
    await assert.doesNotReject(fs.stat(first.runDir));
    await assert.doesNotReject(fs.stat(second.runDir));
    await assert.doesNotReject(fs.stat(first.screenshotsDir));
    await assert.doesNotReject(fs.stat(second.screenshotsDir));
  } finally {
    await fs.rm(outDir, { recursive: true, force: true });
  }
});
