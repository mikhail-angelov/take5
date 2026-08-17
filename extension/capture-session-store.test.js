import test from 'node:test';
import assert from 'node:assert/strict';
import { createCaptureSessionStore } from './capture-session-store.js';

function createFakeStorage() {
  const data = new Map();
  return {
    data,
    async get(key) {
      return data.has(key) ? { [key]: data.get(key) } : {};
    },
    async set(entries) {
      for (const [key, value] of Object.entries(entries)) {
        data.set(key, value);
      }
    },
    async remove(key) {
      data.delete(key);
    },
  };
}

test('capture session store persists and reads a session per tab', async () => {
  const storage = createFakeStorage();
  const store = createCaptureSessionStore(storage);

  assert.equal(await store.get(7), null);

  await store.set(7, { capturing: true, bundle: { steps: [1, 2] } });
  const session = await store.get(7);
  assert.deepEqual(session, { capturing: true, bundle: { steps: [1, 2] } });

  await store.set(7, { capturing: false, bundle: { steps: [1, 2, 3] } });
  assert.equal((await store.get(7)).bundle.steps.length, 3);
  assert.equal((await store.get(7)).capturing, false);
});

test('capture session store isolates tabs and clears sessions', async () => {
  const storage = createFakeStorage();
  const store = createCaptureSessionStore(storage);

  await store.set(1, { capturing: true, bundle: { steps: [] } });
  await store.set(2, { capturing: true, bundle: { steps: [] } });

  await store.clear(1);
  assert.equal(await store.get(1), null);
  assert.notEqual(await store.get(2), null);
});

test('capture session store ignores missing tab ids', async () => {
  const store = createCaptureSessionStore(createFakeStorage());
  await store.set(null, { capturing: true });
  assert.equal(await store.get(null), null);
});
