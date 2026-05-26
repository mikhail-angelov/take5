import test from 'node:test';
import assert from 'node:assert/strict';
import { createToolbarVisibilityController } from './content-script.js';

function createNode() {
  return {
    style: {
      display: '',
    },
  };
}

test('toolbar visibility controller shows the panel and hides the launcher', () => {
  const frame = createNode();
  const launcher = createNode();
  frame.style.display = 'none';
  launcher.style.display = 'block';

  const controller = createToolbarVisibilityController({ frame, launcher });
  controller.show();

  assert.equal(frame.style.display, 'block');
  assert.equal(launcher.style.display, 'none');
});

test('toolbar visibility controller hides the panel and shows the launcher', () => {
  const frame = createNode();
  const launcher = createNode();
  frame.style.display = 'block';
  launcher.style.display = 'none';

  const controller = createToolbarVisibilityController({ frame, launcher });
  controller.hide();

  assert.equal(frame.style.display, 'none');
  assert.equal(launcher.style.display, 'block');
});
