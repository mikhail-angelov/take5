import test from 'node:test';
import assert from 'node:assert/strict';
import { createOpenOnLoadState } from './open-on-load-state.js';

test('open-on-load state consumes a pending tab exactly once', () => {
  const state = createOpenOnLoadState();

  state.requestOpenOnNextLoad(42);

  assert.equal(state.consumePendingOpen(42), true);
  assert.equal(state.consumePendingOpen(42), false);
});

test('open-on-load state ignores invalid tab ids', () => {
  const state = createOpenOnLoadState();

  state.requestOpenOnNextLoad(null);
  state.requestOpenOnNextLoad(undefined);
  state.requestOpenOnNextLoad('42');

  assert.equal(state.consumePendingOpen(null), false);
  assert.equal(state.consumePendingOpen(undefined), false);
  assert.equal(state.consumePendingOpen('42'), false);
});
