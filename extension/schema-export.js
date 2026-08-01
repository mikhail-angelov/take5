import { validateCaptureBundle } from './capture-schema.js';

function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function unwrapBundle(candidate) {
  if (isPlainObject(candidate?.bundle)) {
    return candidate.bundle;
  }

  return candidate;
}

export function serializeScenario(scenario) {
  const bundle = unwrapBundle(scenario);
  validateCaptureBundle(bundle);
  return JSON.stringify(bundle, null, 2);
}

export function parseScenarioJson(jsonText) {
  if (typeof jsonText !== 'string') {
    throw new Error('Scenario JSON must be a string');
  }

  const source = jsonText.replace(/^\uFEFF/, '');

  let parsed;
  try {
    parsed = JSON.parse(source);
  } catch (error) {
    throw new Error(`Scenario JSON is not valid JSON: ${error.message}`, { cause: error });
  }

  return validateCaptureBundle(parsed);
}

function sanitizeFilenamePart(value) {
  const raw = isNonEmptyString(value) ? value.trim() : 'scenario';
  const sanitized = raw
    // Strip filesystem-hostile characters, including control chars, from filenames.
    // eslint-disable-next-line no-control-regex
    .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, ' ')
    .replace(/[^A-Za-z0-9._ -]+/g, ' ')
    .replace(/[-\s]+/g, ' ')
    .replace(/^\.+/, '')
    .replace(/\.+$/, '')
    .trim()
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+/, '')
    .replace(/-+$/, '');

  return sanitized.length > 0 ? sanitized.slice(0, 80) : 'scenario';
}

export function buildScenarioExportFilename(scenario) {
  const candidate =
    scenario?.name ??
    scenario?.title ??
    scenario?.id ??
    scenario?.metadata?.scenarioId ??
    scenario?.bundle?.metadata?.scenarioId ??
    'scenario';

  return `take5-${sanitizeFilenamePart(candidate)}.json`;
}
