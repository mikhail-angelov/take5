import fs from 'node:fs/promises';
import { validateCaptureBundle } from './capture-schema.js';

export async function loadCaptureBundle(filePath) {
  const raw = await fs.readFile(filePath, 'utf8');
  const parsed = JSON.parse(raw.replace(/^\uFEFF/, ''));
  return validateCaptureBundle(parsed);
}
