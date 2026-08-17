import test from 'node:test';
import assert from 'node:assert/strict';
import { createToolbarVisibilityState } from './toolbar-visibility-state.js';

test('toolbar visibility state starts hidden and toggles per tab', () => {
  const state = createToolbarVisibilityState();

  assert.equal(state.isVisible(42), false);
  assert.equal(state.toggle(42), true);
  assert.equal(state.isVisible(42), true);
  assert.equal(state.isVisible(7), false);
  assert.equal(state.toggle(42), false);
  assert.equal(state.isVisible(42), false);
});

test('toolbar visibility state keeps a tab visible until it is forgotten', () => {
  const state = createToolbarVisibilityState();

  state.setVisible(42, true);
  assert.equal(state.isVisible(42), true);
  assert.equal(state.forget(42), true);
  assert.equal(state.isVisible(42), false);
  assert.equal(state.forget(42), false);
});

test('toolbar visibility state ignores invalid tab ids', () => {
  const state = createToolbarVisibilityState();

  assert.equal(state.setVisible(null, true), false);
  assert.equal(state.setVisible('42', true), false);
  assert.equal(state.toggle(undefined), false);
  assert.equal(state.isVisible('42'), false);
  assert.equal(state.forget(null), false);
});
