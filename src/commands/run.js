import fs from 'node:fs/promises';
import { createLogger } from '../logger.js';

export async function runCommand({ captureFile, outDir, debug, cutDelays }) {
  await fs.mkdir(outDir, { recursive: true });

  const logger = createLogger({ debug });
  logger.info('Take5 run');
  logger.info(`capture: ${captureFile}`);
  logger.info(`out: ${outDir}`);
  logger.info(`debug: ${debug}`);
  logger.info(`cutDelays: ${cutDelays}`);
}
