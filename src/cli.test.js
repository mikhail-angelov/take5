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

test('parseCliArgs accepts default run without explicit command', () => {
  const result = parseCliArgs(['fixtures/sample-capture.json']);

  assert.deepEqual(result, {
    command: 'run',
    captureFile: 'fixtures/sample-capture.json',
    outDir: './take5-output',
    debug: false,
    cutDelays: false,
  });
});

test('parseCliArgs rejects unknown flags with CliUsageError', () => {
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

test('cli run path rejects out target that already exists as a file', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureFile = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');
  const outTarget = path.resolve(testDir, '..', 'package.json');

  const result = spawnSync(process.execPath, [cliPath, 'run', captureFile, '--out', outTarget], {
    encoding: 'utf8',
  });

  assert.notEqual(result.status, 0);
  assert.match(result.stderr || result.stdout, /Output path must be a directory:/);
});

test('cli run path rejects capture directories', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureDir = path.resolve(testDir, '..', 'extension');
  const outDir = path.resolve(testDir, '..', 'take5-output-dir-test');

  await fs.rm(outDir, { recursive: true, force: true });

  const result = spawnSync(process.execPath, [cliPath, 'run', captureDir, '--out', outDir], {
    encoding: 'utf8',
  });

  assert.notEqual(result.status, 0);
  assert.match(result.stderr || result.stdout, /Capture file must be a regular file:/);
});

test('cli run path creates output and forwards debug logging', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureFile = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');
  const outDir = path.resolve(testDir, '..', 'take5-output-test');

  await fs.rm(outDir, { recursive: true, force: true });

  const result = spawnSync(process.execPath, [cliPath, captureFile, '--out', outDir, '--debug'], {
    encoding: 'utf8',
  });

  assert.equal(result.status, 0);
  await assert.doesNotReject(fs.stat(outDir));
  assert.match(result.stdout, /Debug logging enabled/);
  assert.match(result.stdout, /cutDelays: false/);
});
