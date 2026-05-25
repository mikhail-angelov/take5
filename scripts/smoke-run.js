#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const cliPath = path.resolve(repoRoot, 'src/cli.js');
const captureFile = path.resolve(repoRoot, 'fixtures/sample-capture.json');
const outDir = path.resolve(repoRoot, 'take5-output-smoke');

const result = spawnSync(process.execPath, [cliPath, 'run', captureFile, '--out', outDir], {
  encoding: 'utf8',
  cwd: repoRoot,
});

if (result.stdout) {
  process.stdout.write(result.stdout);
}

if (result.stderr) {
  process.stderr.write(result.stderr);
}

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}
