const SUPPORTED_STEP_TYPES = new Set([
  'navigate',
  'click',
  'fill',
  'select',
  'keypress',
  'scroll',
  'wait',
  'assert_text',
]);

function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function validateCaptureBundle(bundle) {
  if (!isPlainObject(bundle)) {
    throw new Error('Capture bundle must be an object');
  }

  if (!isPlainObject(bundle.metadata)) {
    throw new Error('Capture bundle metadata must be an object');
  }

  if (typeof bundle.metadata.schemaVersion !== 'number') {
    throw new Error('Capture bundle metadata.schemaVersion must be a number');
  }

  if (typeof bundle.metadata.scenarioId !== 'string' || bundle.metadata.scenarioId.length === 0) {
    throw new Error('Capture bundle metadata.scenarioId must be a non-empty string');
  }

  if (!Array.isArray(bundle.steps) || bundle.steps.length === 0) {
    throw new Error('Capture bundle steps must be a non-empty array');
  }

  if (!Array.isArray(bundle.annotations)) {
    throw new Error('Capture bundle annotations must be an array');
  }

  if (!isPlainObject(bundle.debug)) {
    throw new Error('Capture bundle debug must be an object');
  }

  for (const step of bundle.steps) {
    if (!isPlainObject(step)) {
      throw new Error('Capture bundle steps must contain objects');
    }

    if (typeof step.type !== 'string' || step.type.length === 0) {
      throw new Error('Capture bundle steps must include a type');
    }

    if (!SUPPORTED_STEP_TYPES.has(step.type)) {
      throw new Error(`Unsupported step type: ${step.type}`);
    }
  }

  return bundle;
}

