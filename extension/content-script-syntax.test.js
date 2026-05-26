import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

test('content-script.js parses as a classic script', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const scriptPath = path.resolve(testDir, 'content-script.js');
  const raw = await fs.readFile(scriptPath, 'utf8');

  assert.doesNotThrow(() => {
    new vm.Script(raw, { filename: 'content-script.js' });
  });
});
