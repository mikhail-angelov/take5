#!/usr/bin/env node
import { runCommand } from '../src/commands/run.js';

await runCommand({
  captureFile: 'fixtures/sample-capture.json',
  outDir: './take5-output-smoke',
  debug: false,
  cutDelays: false,
});
