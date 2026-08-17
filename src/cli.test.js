import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { parseCliArgs } from './cli.js';
import { CliUsageError } from './errors.js';

test('parseCliArgs parses the run command shell options', () => {
  const result = parseCliArgs([
    'run',
    'fixtures/sample-capture.json',
    '--out',
    './take5-output',
    '--debug',
  ]);

  assert.deepEqual(result, {
    command: 'run',
    captureFile: 'fixtures/sample-capture.json',
    outDir: './take5-output',
    debug: true,
    cutDelays: false,
    render: false,
    aiCaptions: false,
    userDataDir: null,
    authUrl: null,
    pat: null,
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
    render: false,
    aiCaptions: false,
    userDataDir: null,
    authUrl: null,
    pat: null,
  });
});

test('parseCliArgs parses the --render and --ai-captions flags', () => {
  const result = parseCliArgs(['run', 'fixtures/sample-capture.json', '--render', '--ai-captions']);
  assert.equal(result.render, true);
  assert.equal(result.aiCaptions, true);
});

test('parseCliArgs parses --user-data-dir with a value', () => {
  const result = parseCliArgs([
    'run',
    'fixtures/sample-capture.json',
    '--user-data-dir',
    '/tmp/profile',
  ]);
  assert.equal(result.userDataDir, '/tmp/profile');
});

test('parseCliArgs parses --pat with a value', () => {
  const result = parseCliArgs(['run', 'fixtures/sample-capture.json', '--pat', 'pat_abc123']);
  assert.equal(result.pat, 'pat_abc123');
});

test('parseCliArgs rejects --pat without a token', () => {
  assert.throws(
    () => parseCliArgs(['run', 'fixtures/sample-capture.json', '--pat']),
    /--pat flag requires a token/,
  );
});

test('parseCliArgs rejects --user-data-dir without a path', () => {
  assert.throws(
    () => parseCliArgs(['run', 'fixtures/sample-capture.json', '--user-data-dir']),
    /--user-data-dir flag requires a path/,
  );
});

test('parseCliArgs rejects unknown flags with CliUsageError', () => {
  assert.throws(
    () => parseCliArgs(['run', 'fixtures/sample-capture.json', '--nope']),
    CliUsageError,
  );
});

test('parseCliArgs rejects malformed --out', () => {
  assert.throws(
    () => parseCliArgs(['run', 'fixtures/sample-capture.json', '--out']),
    /--out flag requires a directory/,
  );
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
  assert.match(result.stderr || result.stdout, /Capture file is not readable:/);
});

test('cli run path wraps malformed capture errors without raw stacks', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureDir = await fs.mkdtemp(path.join(os.tmpdir(), 'take5-malformed-capture-'));
  const captureFile = path.join(captureDir, `capture-${randomUUID()}.json`);
  const outDir = path.resolve(testDir, '..', 'take5-output-malformed-capture');

  await fs.writeFile(captureFile, '{"metadata":');
  await fs.rm(outDir, { recursive: true, force: true });

  try {
    const result = spawnSync(process.execPath, [cliPath, 'run', captureFile, '--out', outDir], {
      encoding: 'utf8',
    });

    assert.notEqual(result.status, 0);
    assert.match(result.stderr || result.stdout, /Failed to load capture bundle .*: /);
    assert.doesNotMatch(result.stderr || result.stdout, /at async|node:internal/);
  } finally {
    await fs.rm(captureDir, { recursive: true, force: true });
    await fs.rm(outDir, { recursive: true, force: true });
  }
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

test('cli run path rejects output directories under a file parent', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureFile = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');
  const parentDir = path.resolve(testDir, '..', 'take5-output-unwritable');
  const outTarget = path.resolve(parentDir, 'nested-output');

  await fs.rm(parentDir, { recursive: true, force: true });
  await fs.mkdir(parentDir, { recursive: true });
  await fs.chmod(parentDir, 0o555);

  try {
    const result = spawnSync(process.execPath, [cliPath, 'run', captureFile, '--out', outTarget], {
      encoding: 'utf8',
    });

    assert.notEqual(result.status, 0);
    assert.match(result.stderr || result.stdout, /Unable to create output directory:/);
    assert.doesNotMatch(result.stderr || result.stdout, /at async|node:internal/);
  } finally {
    await fs.chmod(parentDir, 0o755);
    await fs.rm(parentDir, { recursive: true, force: true });
  }
});

test('cli run path wraps artifact write failures without raw stacks', async () => {
  const testDir = path.dirname(fileURLToPath(import.meta.url));
  const cliPath = path.resolve(testDir, 'cli.js');
  const captureFile = path.resolve(testDir, '..', 'fixtures', 'sample-capture.json');
  const outDir = path.resolve(testDir, '..', 'take5-output-artifact-write-failure');

  await fs.rm(outDir, { recursive: true, force: true });
  await fs.mkdir(outDir, { recursive: true });
  await fs.chmod(outDir, 0o555);

  try {
    const result = spawnSync(process.execPath, [cliPath, 'run', captureFile, '--out', outDir], {
      encoding: 'utf8',
    });

    assert.notEqual(result.status, 0);
    assert.match(result.stderr || result.stdout, /Failed to write artifacts to .*: /);
    assert.doesNotMatch(result.stderr || result.stdout, /at async|node:internal/);
  } finally {
    await fs.chmod(outDir, 0o755);
    await fs.rm(outDir, { recursive: true, force: true });
  }
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
