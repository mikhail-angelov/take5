#!/usr/bin/env node
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { realpathSync } from 'node:fs';
import { runCommand } from './commands/run.js';
import { CliUsageError } from './errors.js';

export function parseCliArgs(argv = []) {
  const [command = 'run', ...rest] = argv;

  if (command !== 'run') {
    throw new CliUsageError(`Unknown command: ${command}`);
  }

  const result = {
    command: 'run',
    captureFile: null,
    outDir: './take5-output',
    debug: false,
    cutDelays: false,
  };

  for (let index = 0; index < rest.length; index += 1) {
    const token = rest[index];

    if (!result.captureFile && !token.startsWith('-')) {
      result.captureFile = token;
      continue;
    }

    if (token === '--out') {
      const next = rest[index + 1];
      if (!next || next.startsWith('-')) {
        throw new CliUsageError('--out flag requires a directory');
      }
      result.outDir = next;
      index += 1;
      continue;
    }

    if (token === '--debug') {
      result.debug = true;
      continue;
    }

    if (token === '--cut-delays') {
      result.cutDelays = true;
      continue;
    }

    throw new CliUsageError(`Unknown flag: ${token}`);
  }

  if (!result.captureFile) {
    throw new CliUsageError('run command requires a capture file');
  }

  return result;
}

export async function main(argv = process.argv.slice(2)) {
  const parsed = parseCliArgs(argv);
  await runCommand({
    captureFile: parsed.captureFile,
    outDir: path.resolve(parsed.outDir),
    debug: parsed.debug,
    cutDelays: parsed.cutDelays,
  });
}

const cliPath = realpathSync(fileURLToPath(import.meta.url));
const argvPath = process.argv[1] ? realpathSync(process.argv[1]) : null;
if (argvPath && argvPath === cliPath) {
  try {
    await main();
  } catch (error) {
    if (error instanceof CliUsageError) {
      console.error(error.message);
      process.exitCode = 1;
    } else {
      throw error;
    }
  }
}
