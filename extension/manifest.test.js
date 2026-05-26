import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

async function loadManifest() {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const manifestPath = path.resolve(testDir, 'manifest.json');
  const raw = await fs.readFile(manifestPath, 'utf8');
  return JSON.parse(raw);
}

test('manifest exposes content-script runtime modules as web-accessible resources', async () => {
  const manifest = await loadManifest();
  const resources = manifest.web_accessible_resources?.flatMap((entry) => entry.resources ?? []) ?? [];

  assert.deepEqual(
    resources.filter((resource) =>
      [
        'annotation-overlay.js',
        'capture-engine.js',
        'capture-schema.js',
        'replay-engine.js',
        'plan-generator.js',
      ].includes(resource),
    ),
    [
      'annotation-overlay.js',
      'capture-engine.js',
      'capture-schema.js',
      'replay-engine.js',
      'plan-generator.js',
    ],
  );
});

test('manifest wires the browser action to a popup', async () => {
  const manifest = await loadManifest();

  assert.equal(manifest.action?.default_popup, 'popup.html');
});
