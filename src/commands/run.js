import fs from 'node:fs/promises';
import { constants as fsConstants } from 'node:fs';
import { createLogger } from '../logger.js';
import { CliOperationError } from '../errors.js';

export async function runCommand({ captureFile, outDir, debug, cutDelays }) {
  try {
    await fs.access(captureFile, fsConstants.R_OK);
  } catch {
    throw new CliOperationError(`Capture file is not readable: ${captureFile}`);
  }

  await fs.mkdir(outDir, { recursive: true });

  const logger = createLogger({ debug });
  logger.debug('Debug logging enabled');
  logger.info('Take5 run');
  logger.info(`capture: ${captureFile}`);
  logger.info(`out: ${outDir}`);
  logger.info(`debug: ${debug}`);
  logger.info(`cutDelays: ${cutDelays}`);
}
