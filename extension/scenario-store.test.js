import test from 'node:test';
import assert from 'node:assert/strict';
import { createScenarioStore } from './scenario-store.js';

function createMemoryAdapter(initialScenarios = []) {
  let scenarios = structuredClone(initialScenarios);

  return {
    async readScenarios() {
      return structuredClone(scenarios);
    },
    async writeScenarios(nextScenarios) {
      scenarios = structuredClone(nextScenarios);
    },
  };
}

function createControlledMemoryAdapter(initialScenarios = []) {
  let scenarios = structuredClone(initialScenarios);
  let currentWriteGate = Promise.resolve();
  let releaseCurrentWrite = () => {};

  return {
    async readScenarios() {
      return structuredClone(scenarios);
    },
    async writeScenarios(nextScenarios) {
      await currentWriteGate;
      scenarios = structuredClone(nextScenarios);
    },
    blockNextWrite() {
      currentWriteGate = new Promise((resolve) => {
        releaseCurrentWrite = resolve;
      });
    },
    releaseWrite() {
      releaseCurrentWrite();
      currentWriteGate = Promise.resolve();
    },
    getScenarios() {
      return structuredClone(scenarios);
    },
  };
}

const scenarioBundle = {
  metadata: {
    schemaVersion: 1,
    scenarioId: 'scenario-one',
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

test('scenario store lists, saves, and deletes scenarios', async () => {
  const store = createScenarioStore(createMemoryAdapter());

  assert.deepEqual(await store.listScenarios(), []);

  const saved = await store.saveScenario(scenarioBundle);
  assert.equal(saved.id, 'scenario-one');
  assert.equal(saved.stepCount, 1);
  assert.equal(saved.annotationCount, 0);
  assert.equal(saved.bundle.metadata.scenarioId, 'scenario-one');

  const listed = await store.listScenarios();
  assert.equal(listed.length, 1);
  assert.equal(listed[0].id, 'scenario-one');

  assert.equal(await store.deleteScenario('scenario-one'), true);
  assert.deepEqual(await store.listScenarios(), []);
  assert.equal(await store.deleteScenario('scenario-one'), false);
});

test('scenario store preserves createdAt when updating an existing scenario', async () => {
  const times = [
    '2026-05-26T10:00:00.000Z',
    '2026-05-26T11:00:00.000Z',
  ];
  const store = createScenarioStore(createMemoryAdapter(), {
    now: () => times.shift(),
  });

  const firstSave = await store.saveScenario(scenarioBundle);
  const secondSave = await store.saveScenario({
    ...scenarioBundle,
    steps: [
      ...scenarioBundle.steps,
      {
        type: 'wait',
        ms: 250,
      },
    ],
  });

  assert.equal(firstSave.createdAt, '2026-05-26T10:00:00.000Z');
  assert.equal(secondSave.createdAt, '2026-05-26T10:00:00.000Z');
  assert.equal(secondSave.updatedAt, '2026-05-26T11:00:00.000Z');
  assert.equal(secondSave.stepCount, 2);
});

test('scenario store serializes overlapping mutations and preserves both outcomes', async () => {
  const adapter = createControlledMemoryAdapter([
    {
      id: 'scenario-one',
      name: 'Old scenario',
      createdAt: '2026-05-26T09:00:00.000Z',
      updatedAt: '2026-05-26T09:00:00.000Z',
      stepCount: 1,
      annotationCount: 0,
      bundle: structuredClone(scenarioBundle),
    },
  ]);
  const store = createScenarioStore(adapter, {
    now: () => '2026-05-26T12:00:00.000Z',
  });

  adapter.blockNextWrite();

  const savePromise = store.saveScenario({
    ...scenarioBundle,
    metadata: {
      ...scenarioBundle.metadata,
      scenarioId: 'new-scenario',
    },
  });
  const deletePromise = store.deleteScenario('scenario-one');

  adapter.releaseWrite();
  adapter.blockNextWrite();
  adapter.releaseWrite();

  await Promise.all([savePromise, deletePromise]);

  const listed = await store.listScenarios();
  assert.deepEqual(
    listed.map((scenario) => scenario.id),
    ['new-scenario'],
  );
});

test('scenario store skips corrupt stored records instead of throwing', async () => {
  const store = createScenarioStore(
    createMemoryAdapter([
      null,
      {
        id: 'broken-record',
        bundle: {
          metadata: {
            schemaVersion: 1,
            scenarioId: 'broken-record',
          },
          steps: [],
          annotations: [],
          debug: {},
        },
      },
      {
        id: 'valid-record',
        name: 'Valid record',
        createdAt: '2026-05-26T09:00:00.000Z',
        updatedAt: '2026-05-26T09:05:00.000Z',
        bundle: structuredClone(scenarioBundle),
      },
    ]),
  );

  const listed = await store.listScenarios();
  assert.equal(listed.length, 1);
  assert.equal(listed[0].id, 'scenario-one');
  assert.equal(listed[0].name, 'Valid record');
});
