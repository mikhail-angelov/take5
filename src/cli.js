#!/usr/bin/env node
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { realpathSync } from 'node:fs';
import { runCommand } from './commands/run.js';
import { CliOperationError, CliUsageError } from './errors.js';

export function parseCliArgs(argv = []) {
  const [firstArg, ...rest] = argv;
  const hasExplicitRun = firstArg === 'run';
  const command = 'run';
  const captureTokens = hasExplicitRun ? rest : argv;

  const result = {
    command,
    captureFile: null,
    outDir: './take5-output',
    debug: false,
    cutDelays: false,
    render: false,
    aiCaptions: false,
    userDataDir: null,
    authUrl: null,
    pat: null,
  };

  for (let index = 0; index < captureTokens.length; index += 1) {
    const token = captureTokens[index];

    if (!result.captureFile && !token.startsWith('-')) {
      result.captureFile = token;
      continue;
    }

    if (token === '--out') {
      const next = captureTokens[index + 1];
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

    if (token === '--render') {
      result.render = true;
      continue;
    }

    if (token === '--ai-captions') {
      result.aiCaptions = true;
      continue;
    }

    if (token === '--user-data-dir') {
      const next = captureTokens[index + 1];
      if (!next || next.startsWith('-')) {
        throw new CliUsageError('--user-data-dir flag requires a path');
      }
      result.userDataDir = next;
      index += 1;
      continue;
    }

    if (token === '--auth-url') {
      const next = captureTokens[index + 1];
      if (!next || next.startsWith('-')) {
        throw new CliUsageError('--auth-url flag requires a URL');
      }
      result.authUrl = next;
      index += 1;
      continue;
    }

    if (token === '--pat') {
      const next = captureTokens[index + 1];
      if (!next || next.startsWith('-')) {
        throw new CliUsageError('--pat flag requires a token');
      }
      result.pat = next;
      index += 1;
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
    render: parsed.render,
    aiCaptions: parsed.aiCaptions,
    userDataDir: parsed.userDataDir,
    authUrl: parsed.authUrl,
    // Prefer an env var so the secret need not appear in argv / shell history.
    pat: parsed.pat ?? process.env.TAKE5_PAT ?? null,
  });
}

const cliPath = realpathSync(fileURLToPath(import.meta.url));
const argvPath = process.argv[1] ? realpathSync(process.argv[1]) : null;
if (argvPath && argvPath === cliPath) {
  try {
    await main();
  } catch (error) {
    if (error instanceof CliUsageError || error instanceof CliOperationError) {
      console.error(error.message);
      process.exitCode = 1;
    } else {
      throw error;
    }
  }
}
