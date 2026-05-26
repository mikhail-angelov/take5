import test from 'node:test';
import assert from 'node:assert/strict';
import { createPromptInteractionController } from './annotation-overlay.js';

function createNode() {
  const listeners = new Map();
  return {
    listeners,
    addEventListener(type, handler) {
      const handlers = listeners.get(type) ?? [];
      handlers.push(handler);
      listeners.set(type, handlers);
    },
    dispatch(type, event = {}) {
      for (const handler of listeners.get(type) ?? []) {
        handler({
          target: event.target ?? this,
          key: event.key,
          metaKey: Boolean(event.metaKey),
          ctrlKey: Boolean(event.ctrlKey),
          preventDefault() {},
          stopPropagation() {},
        });
      }
    },
    remove() {
      this.removed = true;
    },
  };
}

test('prompt interaction controller resolves null on Escape', async () => {
  const dialog = createNode();
  const panel = createNode();
  const input = createNode();
  input.value = 'hello';
  const save = createNode();
  const cancel = createNode();

  const pending = createPromptInteractionController({ dialog, panel, input, save, cancel });
  dialog.dispatch('keydown', { key: 'Escape' });

  assert.equal(await pending, null);
  assert.equal(dialog.removed, true);
});

test('prompt interaction controller resolves null on outside click', async () => {
  const dialog = createNode();
  const panel = createNode();
  const input = createNode();
  input.value = 'hello';
  const save = createNode();
  const cancel = createNode();

  const pending = createPromptInteractionController({ dialog, panel, input, save, cancel });
  dialog.dispatch('click', { target: dialog });

  assert.equal(await pending, null);
  assert.equal(dialog.removed, true);
});

test('prompt interaction controller preserves textarea input and trims on save', async () => {
  const dialog = createNode();
  const panel = createNode();
  const input = createNode();
  input.value = '  hello annotation  ';
  const save = createNode();
  const cancel = createNode();

  const pending = createPromptInteractionController({ dialog, panel, input, save, cancel });
  save.dispatch('click');

  assert.equal(await pending, 'hello annotation');
  assert.equal(dialog.removed, true);
});
