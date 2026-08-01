import test from 'node:test';
import assert from 'node:assert/strict';
import { createReplayEngine } from './replay-engine.js';

function createFakeOverlay() {
  const calls = [];
  return {
    calls,
    clear() {
      calls.push(['clear']);
    },
    showStep(step) {
      calls.push(['showStep', step.id]);
    },
    showAnnotations(annotations) {
      calls.push(['showAnnotations', annotations.map((annotation) => annotation.description)]);
    },
    setReplayMode(mode) {
      calls.push(['setReplayMode', mode]);
    },
  };
}

const bundle = {
  metadata: {
    schemaVersion: 1,
    scenarioId: 'replay-demo',
  },
  steps: [
    { type: 'navigate', url: 'https://example.com/app' },
    { type: 'click', selector: '#launch' },
    { type: 'wait', ms: 5 },
  ],
  annotations: [
    {
      description: 'Check the launch button',
      selector: '#launch',
      stepIndex: 1,
    },
  ],
  debug: {},
};

test('createReplayEngine auto plays semantic steps in order', async () => {
  const overlay = createFakeOverlay();
  const executed = [];
  const engine = createReplayEngine({
    overlay,
    runStep: async (step) => {
      executed.push(step.type);
    },
    delay: async () => {},
  });

  await engine.load(bundle);
  await engine.play();

  assert.deepEqual(executed, ['navigate', 'click', 'wait']);
  assert.equal(engine.getState().mode, 'idle');
  assert.equal(engine.getState().index, 3);
  assert.deepEqual(
    overlay.calls.map((call) => call[0]),
    [
      'clear',
      'setReplayMode',
      'showAnnotations',
      'setReplayMode',
      'showStep',
      'showStep',
      'showStep',
      'setReplayMode',
    ],
  );
});

test('createReplayEngine supports step-through navigation', async () => {
  const overlay = createFakeOverlay();
  const executed = [];
  const engine = createReplayEngine({
    overlay,
    runStep: async (step) => {
      executed.push(step.type);
    },
    delay: async () => {},
  });

  await engine.load(bundle);
  await engine.next();
  await engine.next();
  await engine.back();

  assert.deepEqual(executed, ['navigate', 'click']);
  assert.equal(engine.getState().index, 1);
  assert.equal(engine.getState().mode, 'step');
});
