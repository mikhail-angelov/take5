import test from 'node:test';
import assert from 'node:assert/strict';
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
