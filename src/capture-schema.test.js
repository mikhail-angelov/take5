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

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        metadata: { ...validBundle.metadata, schemaVersion: 1.5 },
      }),
    /schemaVersion must be a positive integer/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        metadata: { ...validBundle.metadata, schemaVersion: Number.POSITIVE_INFINITY },
      }),
    /schemaVersion must be a positive integer/,
  );
});

test('validateCaptureBundle validates per-step requirements', () => {
  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'navigate' }],
      }),
    /navigate step requires a non-empty url/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'click' }],
      }),
    /click step requires ref or selector/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'fill', selector: '#email' }],
      }),
    /fill step requires ref or selector plus value/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'select', ref: 'e1' }],
      }),
    /select step requires ref or selector plus value/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'keypress' }],
      }),
    /keypress step requires a non-empty key/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'scroll', direction: 'down' }],
      }),
    /scroll step requires direction and amount/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'wait', ms: Number.NaN }],
      }),
    /wait step requires numeric ms/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        steps: [{ type: 'assert_text', selector: '#status' }],
      }),
    /assert_text step requires ref or selector plus text/,
  );
});

test('validateCaptureBundle validates annotation entries', () => {
  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        annotations: ['freeform'],
      }),
    /annotations must contain objects/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        annotations: [{ selector: '#target' }],
      }),
    /annotations require a non-empty description/,
  );

  assert.throws(
    () =>
      validateCaptureBundle({
        ...validBundle,
        annotations: [{ description: 'Check this' }],
      }),
    /annotations require selector or targetRect/,
  );

  assert.deepEqual(
    validateCaptureBundle({
      ...validBundle,
      annotations: [{ description: 'Check this', selector: '#target' }],
    }).annotations,
    [{ description: 'Check this', selector: '#target' }],
  );
});
