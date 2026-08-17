const SUPPORTED_STEP_TYPES = new Set([
  'navigate',
  'click',
  'fill',
  'select',
  'keypress',
  'scroll',
  'wait',
  'assert_text',
  'pointer_drag',
]);

const SUPPORTED_SCHEMA_VERSIONS = new Set([1, 2]);
const SUPPORTED_SCROLL_DIRECTIONS = new Set(['up', 'down']);

function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function isFiniteNumber(value) {
  return typeof value === 'number' && Number.isFinite(value);
}

function hasLocatorShape(step) {
  return isNonEmptyString(step.ref) || isNonEmptyString(step.selector);
}

function isValidViewport(viewport) {
  return (
    isPlainObject(viewport) &&
    isFiniteNumber(viewport.width) &&
    isFiniteNumber(viewport.height) &&
    viewport.width > 0 &&
    viewport.height > 0
  );
}

function isValidPointerPoint(point, previousTime) {
  return (
    isPlainObject(point) &&
    isFiniteNumber(point.x) &&
    isFiniteNumber(point.y) &&
    isFiniteNumber(point.t) &&
    point.t >= 0 &&
    point.t >= previousTime
  );
}

function validateStep(step) {
  if (!isPlainObject(step)) {
    throw new Error('Capture bundle steps must contain objects');
  }

  if (!isNonEmptyString(step.type)) {
    throw new Error('Capture bundle steps must include a type');
  }

  if (!SUPPORTED_STEP_TYPES.has(step.type)) {
    throw new Error(`Unsupported step type: ${step.type}`);
  }

  switch (step.type) {
    case 'navigate':
      if (!isNonEmptyString(step.url)) {
        throw new Error('navigate step requires a non-empty url');
      }
      break;
    case 'click':
      if (!hasLocatorShape(step)) {
        throw new Error('click step requires ref or selector');
      }
      break;
    case 'fill':
      if (!hasLocatorShape(step) || typeof step.value !== 'string') {
        throw new Error('fill step requires ref or selector plus value');
      }
      if (
        step.typingDelayMs !== undefined &&
        (!isFiniteNumber(step.typingDelayMs) || step.typingDelayMs < 0)
      ) {
        throw new Error('fill step typingDelayMs must be a non-negative number');
      }
      break;
    case 'select':
      if (!hasLocatorShape(step) || typeof step.value !== 'string') {
        throw new Error('select step requires ref or selector plus value');
      }
      break;
    case 'keypress':
      if (!isNonEmptyString(step.key)) {
        throw new Error('keypress step requires a non-empty key');
      }
      break;
    case 'scroll':
      if (
        !SUPPORTED_SCROLL_DIRECTIONS.has(step.direction) ||
        !isFiniteNumber(step.amount) ||
        step.amount <= 0
      ) {
        throw new Error('scroll step requires direction up/down and a positive amount');
      }
      break;
    case 'wait':
      if (!isFiniteNumber(step.ms) || step.ms < 0) {
        throw new Error('wait step requires numeric ms');
      }
      break;
    case 'assert_text':
      if (!hasLocatorShape(step) || !isNonEmptyString(step.text)) {
        throw new Error('assert_text step requires ref or selector plus text');
      }
      break;
    case 'pointer_drag': {
      if (!Array.isArray(step.points) || step.points.length < 2) {
        throw new Error('pointer_drag step requires at least two timed points');
      }
      let previousTime = 0;
      for (const point of step.points) {
        if (!isValidPointerPoint(point, previousTime)) {
          throw new Error(
            'pointer_drag points require finite x/y and non-decreasing non-negative t',
          );
        }
        previousTime = point.t;
      }
      break;
    }
    default:
      break;
  }
}

function validateAnnotation(annotation) {
  if (!isPlainObject(annotation)) {
    throw new Error('Capture bundle annotations must contain objects');
  }

  if (!isNonEmptyString(annotation.description)) {
    throw new Error('Capture bundle annotations require a non-empty description');
  }

  const hasSelector = isNonEmptyString(annotation.selector);
  const hasTargetRect = annotation.targetRect !== undefined && annotation.targetRect !== null;

  if (hasTargetRect) {
    if (!isPlainObject(annotation.targetRect)) {
      throw new Error('Capture bundle annotations require a valid targetRect');
    }

    if (
      !isFiniteNumber(annotation.targetRect.x) ||
      !isFiniteNumber(annotation.targetRect.y) ||
      !isFiniteNumber(annotation.targetRect.width) ||
      !isFiniteNumber(annotation.targetRect.height) ||
      annotation.targetRect.width <= 0 ||
      annotation.targetRect.height <= 0
    ) {
      throw new Error('Capture bundle annotations require a valid targetRect');
    }
  }

  if (!hasSelector && !hasTargetRect) {
    throw new Error('Capture bundle annotations require selector or targetRect');
  }
}

export function validateCaptureBundle(bundle) {
  if (!isPlainObject(bundle)) {
    throw new Error('Capture bundle must be an object');
  }

  if (!isPlainObject(bundle.metadata)) {
    throw new Error('Capture bundle metadata must be an object');
  }

  if (!Number.isInteger(bundle.metadata.schemaVersion) || bundle.metadata.schemaVersion < 1) {
    throw new Error('Capture bundle metadata.schemaVersion must be a positive integer');
  }

  if (!SUPPORTED_SCHEMA_VERSIONS.has(bundle.metadata.schemaVersion)) {
    throw new Error(`Unsupported capture bundle schemaVersion: ${bundle.metadata.schemaVersion}`);
  }

  if (bundle.metadata.schemaVersion >= 2 && !isValidViewport(bundle.metadata.viewport)) {
    throw new Error(
      'schemaVersion 2 capture metadata requires a positive viewport width and height',
    );
  }

  if (!isNonEmptyString(bundle.metadata.scenarioId)) {
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
    validateStep(step);
  }

  for (const annotation of bundle.annotations) {
    validateAnnotation(annotation);
  }

  return bundle;
}
