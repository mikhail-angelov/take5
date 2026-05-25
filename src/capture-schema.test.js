import test from 'node:test';
import assert from 'node:assert/strict';
import { validateCaptureBundle } from './capture-schema.js';

const validBundle = {
  metadata: {
    schemaVersion: 1,
    scenarioId: 'sample-scenario',
  },
  steps: [
    {
      type: 'navigate',
      url: 'https://example.com',
    },
  ],
  annotations: [],
  debug: {},
};

test('validateCaptureBundle accepts a minimal valid bundle', () => {
  assert.deepEqual(validateCaptureBundle(validBundle), validBundle);
});

test('validateCaptureBundle rejects unsupported step types', () => {
  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'drag_and_pray' }],
      }),
    /Unsupported step type: drag_and_pray/,
  );
});

test('validateCaptureBundle enforces the required top-level fields', () => {
  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        metadata: { scenarioId: 'missing-schema-version' },
      }),
    /metadata\.schemaVersion/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        metadata: { schemaVersion: 1 },
      }),
    /metadata\.scenarioId/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [],
      }),
    /steps must be a non-empty array/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        annotations: {},
      }),
    /annotations must be an array/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        debug: [],
      }),
    /debug must be an object/,
  );
});

