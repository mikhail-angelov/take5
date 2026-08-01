import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildScenarioExportFilename,
  parseScenarioJson,
  serializeScenario,
} from './schema-export.js';

const bundle = {
  metadata: {
    schemaVersion: 1,
    scenarioId: 'scenario-one',
    baseUrl: 'https://example.com',
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

test('serializeScenario and parseScenarioJson round-trip a valid bundle', () => {
  const json = serializeScenario(bundle);

  assert.match(json, /"scenarioId": "scenario-one"/);
  assert.deepEqual(parseScenarioJson(json), bundle);
});

test('parseScenarioJson rejects malformed or invalid input', () => {
  assert.throws(() => parseScenarioJson('{ not json }'), /not valid JSON/);

  assert.throws(
    () =>
      parseScenarioJson(
        JSON.stringify({
          metadata: {
            schemaVersion: 1,
            scenarioId: 'broken',
          },
          steps: [],
          annotations: [],
          debug: {},
        }),
      ),
    /steps must be a non-empty array/,
  );
});

test('buildScenarioExportFilename sanitizes unsafe scenario identifiers', () => {
  assert.equal(
    buildScenarioExportFilename({
      id: 'folder/name:demo\\draft',
    }),
    'take5-folder-name-demo-draft.json',
  );

  assert.equal(
    buildScenarioExportFilename({
      name: '  ..Final: Walkthrough??  ',
    }),
    'take5-Final-Walkthrough.json',
  );
});
