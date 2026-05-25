import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseCliArgs } from './cli.js';
import { CliUsageError } from './errors.js';

test('parseCliArgs parses the run command shell options', () => {
  const result = parseCliArgs(['run', 'fixtures/sample-capture.json', '--out', './take5-output', '--debug']);

  assert.deepEqual(result, {
    command: 'run',
    captureFile: 'fixtures/sample-capture.json',
    outDir: './take5-output',
    debug: true,
    cutDelays: false,
  });
});

test('parseCliArgs requires a capture file for run', () => {
  assert.throws(() => parseCliArgs(['run']), /run command requires a capture file/);
});

test('parseCliArgs rejects unknown commands and flags with CliUsageError', () => {
  assert.throws(() => parseCliArgs(['dance']), CliUsageError);
  assert.throws(() => parseCliArgs(['run', 'fixtures/sample-capture.json', '--nope']), CliUsageError);
});

test('parseCliArgs rejects malformed --out', () => {
  assert.throws(() => parseCliArgs(['run', 'fixtures/sample-capture.json', '--out']), /--out flag requires a directory/);
});

test('cli smoke path fails for missing capture file', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const missingCapture = path.resolve(testDir, 'does-not-exist.json');
  await fs.rm(path.resolve(testDir, 'take5-output'), { recursive: true, force: true });

  const result = spawnSync(process.execPath, [cliPath, 'run', missingCapture], {
    encoding: 'utf8',
  });

  assert.notEqual(result.status, 0);
  assert.match((result.stderr || result.stdout), /Capture file is not readable:/);
});

test('cli run path creates output and forwards debug logging', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureFile = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');
  const outDir = path.resolve(testDir, '..', 'take5-output-test');

  await fs.rm(outDir, { recursive: true, force: true });

  const result = spawnSync(process.execPath, [cliPath, 'run', captureFile, '--out', outDir, '--debug'], {
    encoding: 'utf8',
  });

  assert.equal(result.status, 0);
  await assert.doesNotReject(fs.stat(outDir));
  assert.match(result.stdout, /Debug logging enabled/);
  assert.match(result.stdout, /cutDelays: false/);
});
