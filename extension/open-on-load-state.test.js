import test from 'node:test';
import assert from 'node:assert/strict';
import { createOpenOnLoadState } from './open-on-load-state.js';

test('open-on-load state consumes a pending tab exactly once', () => {
  const state = createOpenOnLoadState();

  assert.equal(state.enableForTab(42), true);

  assert.equal(state.consumePendingOpen(42), true);
  assert.equal(state.consumePendingOpen(42), false);
  assert.equal(state.isEnabledForTab(42), true);
});

test('open-on-load state disables a tab and clears its pending open flag', () => {
  const state = createOpenOnLoadState();

  state.enableForTab(42);
  assert.equal(state.disableForTab(42), true);
  assert.equal(state.isEnabledForTab(42), false);
  assert.equal(state.consumePendingOpen(42), false);
});

test('open-on-load state ignores invalid tab ids', () => {
  const state = createOpenOnLoadState();

  state.enableForTab(null);
  state.enableForTab(undefined);
  state.enableForTab('42');

  assert.equal(state.consumePendingOpen(null), false);
  assert.equal(state.consumePendingOpen(undefined), false);
  assert.equal(state.consumePendingOpen('42'), false);
  assert.equal(state.disableForTab(null), false);
  assert.equal(state.isEnabledForTab('42'), false);
});
