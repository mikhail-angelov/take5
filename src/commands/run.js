import fs from 'node:fs/promises';
import { constants as fsConstants } from 'node:fs';
import { createLogger } from '../logger.js';
import { CliOperationError } from '../errors.js';
import path from 'node:path';
import { loadCaptureBundle } from '../capture-loader.js';
import { createReplayPlan } from '../plan-generator.js';
import { writeRunArtifacts } from '../artifact-writer.js';
import { replayPlan } from '../replay-runner.js';
import { renderVideo } from '../render.js';
import { buildStepCaptions, naturalizeCaptions } from '../captions.js';

function asCliOperationError(prefix, error) {
  if (error instanceof CliOperationError) {
    return error;
  }

  const detail =
    error instanceof Error && typeof error.message === 'string' ? error.message : String(error);
  return new CliOperationError(`${prefix}: ${detail}`);
}

async function validateCaptureFile(captureFile) {
  try {
    const stats = await fs.stat(captureFile);
    if (!stats.isFile()) {
      throw new CliOperationError(`Capture file must be a regular file: ${captureFile}`);
    }
    await fs.access(captureFile, fsConstants.R_OK);
  } catch (error) {
    if (error instanceof CliOperationError) {
      throw error;
    }
    throw new CliOperationError(`Capture file is not readable: ${captureFile}`);
  }
}

async function validateOutDir(outDir) {
  try {
    const stats = await fs.stat(outDir);
    if (!stats.isDirectory()) {
      throw new CliOperationError(`Output path must be a directory: ${outDir}`);
    }
  } catch (error) {
    if (error && error.code === 'ENOENT') {
      return;
    }
    if (error instanceof CliOperationError) {
      throw error;
    }
    throw new CliOperationError(`Output path must be a directory: ${outDir}`);
  }
}

export async function runCommand({
  captureFile,
  outDir,
  debug,
  cutDelays,
  render = false,
  aiCaptions = false,
  userDataDir = null,
  authUrl = null,
  pat = null,
}) {
  if (pat && userDataDir) {
    // A PAT bootstrap must run in a fresh, isolated context (SPEC22); a persistent
    // profile carries prior cookies / session that break identity isolation.
    throw new CliOperationError('--pat cannot be combined with --user-data-dir');
  }

  await validateCaptureFile(captureFile);
  await validateOutDir(outDir);

  try {
    await fs.mkdir(outDir, { recursive: true });
  } catch {
    throw new CliOperationError(`Unable to create output directory: ${outDir}`);
  }

  const logger = createLogger({ debug });
  logger.debug('Debug logging enabled');
  logger.info('Take5 run');
  logger.info(`capture: ${captureFile}`);
  logger.info(`out: ${outDir}`);
  logger.info(`debug: ${debug}`);
  logger.info(`cutDelays: ${cutDelays}`);

  let capture;
  try {
    capture = await loadCaptureBundle(captureFile);
  } catch (error) {
    throw asCliOperationError(`Failed to load capture bundle ${captureFile}`, error);
  }

  let plan;
  try {
    plan = createReplayPlan(capture);
  } catch (error) {
    throw asCliOperationError(`Failed to normalize replay plan for ${captureFile}`, error);
  }

  let runDir;
  try {
    ({ runDir } = await writeRunArtifacts({
      outDir,
      capture,
      plan,
    }));
  } catch (error) {
    throw asCliOperationError(`Failed to write artifacts to ${outDir}`, error);
  }

  logger.info(`run: ${runDir}`);

  if (!render) {
    return;
  }

  let replayResult;
  try {
    replayResult = await replayPlan(plan, {
      outputDir: runDir,
      logger,
      cutDelays,
      userDataDir,
      authUrl,
      pat,
      // A real browser profile needs a visible window so a macOS Keychain prompt
      // (to decrypt the profile's cookies) can be approved.
      headless: !userDataDir,
    });
  } catch (error) {
    throw asCliOperationError(`Failed to replay plan for ${captureFile}`, error);
  }

  for (const warning of replayResult.warnings) {
    logger.info(`warning: ${warning}`);
  }

  let captions = buildStepCaptions(plan);
  if (aiCaptions) {
    captions = await naturalizeCaptions(captions, { logger });
  }
  // The auth navigation is an extra meaningful action ahead of the plan, so it
  // would shift every caption by one; prepend an empty caption to absorb it.
  if (authUrl) {
    captions = ['', ...captions];
  }

  const videoPath = path.join(runDir, 'demo.mp4');
  try {
    await renderVideo({ sourceDir: runDir, outPath: videoPath, cutDelays, captions });
  } catch (error) {
    throw asCliOperationError(`Failed to render video for ${captureFile}`, error);
  }

  logger.info(`video: ${videoPath}`);
}
