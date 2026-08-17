import test from 'node:test';
import assert from 'node:assert/strict';
import { createToolbarVisibilityController } from './toolbar-visibility.js';

function createNode() {
  return {
    style: {
      display: '',
    },
  };
}

test('toolbar visibility controller shows the panel and reports it', () => {
  const frame = createNode();
  frame.style.display = 'none';
  const changes = [];

  const controller = createToolbarVisibilityController({
    frame,
    onChange: (visible) => changes.push(visible),
  });
  controller.show();

  assert.equal(frame.style.display, 'block');
  assert.equal(controller.isVisible(), true);
  assert.deepEqual(changes, [true]);
});

test('toolbar visibility controller hides the panel and reports it', () => {
  const frame = createNode();
  frame.style.display = 'block';
  const changes = [];

  const controller = createToolbarVisibilityController({
    frame,
    onChange: (visible) => changes.push(visible),
  });
  controller.hide();

  assert.equal(frame.style.display, 'none');
  assert.equal(controller.isVisible(), false);
  assert.deepEqual(changes, [false]);
});
