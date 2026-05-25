import fs from 'node:fs/promises';
import { constants as fsConstants } from 'node:fs';
import { createLogger } from '../logger.js';
import { CliOperationError } from '../errors.js';

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

export async function runCommand({ captureFile, outDir, debug, cutDelays }) {
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
}
