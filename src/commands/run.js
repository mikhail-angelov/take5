import fs from 'node:fs/promises';
import { constants as fsConstants } from 'node:fs';
import { createLogger } from '../logger.js';

export async function runCommand({ captureFile, outDir, debug, cutDelays }) {
  await fs.access(captureFile, fsConstants.R_OK);
  await fs.mkdir(outDir, { recursive: true });

  const logger = createLogger({ debug });
  logger.debug('Debug logging enabled');
  logger.info('Take5 run');
  logger.info(`capture: ${captureFile}`);
  logger.info(`out: ${outDir}`);
  logger.info(`debug: ${debug}`);
  logger.info(`cutDelays: ${cutDelays}`);
}
