import { validateCaptureBundle } from '../src/capture-schema.js';

function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
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
    throw new Error(`Scenario JSON is not valid JSON: ${error.message}`);
  }

  return validateCaptureBundle(parsed);
}
