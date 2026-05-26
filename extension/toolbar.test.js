import test from 'node:test';
import assert from 'node:assert/strict';
import { buildCaptureStartPayload, createToolbarController } from './toolbar.js';

function createScenario(id, name) {
  return {
    id,
    name,
    stepCount: 1,
    annotationCount: 0,
    bundle: {
      metadata: {
        schemaVersion: 1,
        scenarioId: id,
      },
      steps: [
        {
          type: 'navigate',
          url: 'https://example.com',
        },
      ],
      annotations: [],
      debug: {},
    },
  };
}

test('toolbar controller keeps dirty editor content when selection change is canceled', () => {
  const prompts = [];
  const controller = createToolbarController({
    confirmDiscardChanges(message) {
      prompts.push(message);
      return false;
    },
  });

  controller.setScenarios([createScenario('first', 'First'), createScenario('second', 'Second')]);
  controller.selectScenario('first');
  controller.setEditorValue('{"draft":true}');

  const changed = controller.selectScenario('second');

  assert.equal(changed, false);
  assert.equal(controller.getSelectedScenarioId(), 'first');
  assert.equal(controller.getEditorValue(), '{"draft":true}');
  assert.equal(prompts.length, 1);
});

test('toolbar controller keeps dirty editor content when refresh is canceled', () => {
  const prompts = [];
  const controller = createToolbarController({
    confirmDiscardChanges(message) {
      prompts.push(message);
      return false;
    },
  });

  controller.setScenarios([createScenario('first', 'First')]);
  controller.selectScenario('first');
  controller.setEditorValue('{"draft":true}');

  const changed = controller.refreshScenarios([createScenario('first', 'Updated')]);

  assert.equal(changed, false);
  assert.equal(controller.getEditorValue(), '{"draft":true}');
  assert.equal(controller.getScenarios()[0].name, 'First');
  assert.equal(prompts.length, 1);
});

test('buildCaptureStartPayload defers page metadata to the content script for new captures', () => {
  assert.deepEqual(buildCaptureStartPayload(null), {});
});

test('buildCaptureStartPayload preserves saved scenario identity for recapture', () => {
  assert.deepEqual(
    buildCaptureStartPayload({
      id: 'demo-flow',
      name: 'Demo flow',
    }),
    {
      scenarioId: 'demo-flow',
      name: 'Demo flow',
    },
  );
});
