import { validateCaptureBundle } from './capture-schema.js';

const STORAGE_KEY = 'take5.scenarios';

function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function cloneValue(value) {
  if (typeof structuredClone === 'function') {
    return structuredClone(value);
  }

  return JSON.parse(JSON.stringify(value));
}

function normalizeTimestamp(value, fallback) {
  return isNonEmptyString(value) ? value : fallback;
}

function normalizeDisplayName(source, scenarioId) {
  if (isPlainObject(source) && isNonEmptyString(source.name)) {
    return source.name.trim();
  }

  if (isPlainObject(source) && isNonEmptyString(source.title)) {
    return source.title.trim();
  }

  if (isPlainObject(source?.metadata) && isNonEmptyString(source.metadata.name)) {
    return source.metadata.name.trim();
  }

  return scenarioId;
}

function normalizeScenarioBundle(input) {
  const bundle = isPlainObject(input?.bundle) ? input.bundle : input;

  if (!isPlainObject(bundle)) {
    throw new Error('Scenario must be a capture bundle object');
  }

  const metadata = isPlainObject(bundle.metadata) ? bundle.metadata : {};
  const scenarioId = isNonEmptyString(metadata.scenarioId)
    ? metadata.scenarioId.trim()
    : isNonEmptyString(input?.id)
      ? input.id.trim()
      : null;

  if (!scenarioId) {
    throw new Error('Scenario metadata.scenarioId is required');
  }

  if (!Number.isInteger(metadata.schemaVersion) || metadata.schemaVersion < 1) {
    throw new Error('Scenario metadata.schemaVersion must be a positive integer');
  }

  if (!Array.isArray(bundle.steps)) {
    throw new Error('Scenario steps must be an array');
  }

  if (!Array.isArray(bundle.annotations)) {
    throw new Error('Scenario annotations must be an array');
  }

  if (!isPlainObject(bundle.debug)) {
    throw new Error('Scenario debug must be an object');
  }

  const bundleCopy = cloneValue(bundle);
  bundleCopy.metadata = {
    ...bundleCopy.metadata,
    scenarioId,
  };

  validateCaptureBundle(bundleCopy);

  return bundleCopy;
}

function normalizeScenarioRecord(record, existingRecord, now) {
  const bundle = normalizeScenarioBundle(record);
  const scenarioId = bundle.metadata.scenarioId;
  const createdAt = normalizeTimestamp(
    existingRecord?.createdAt ?? record?.createdAt ?? bundle.metadata.createdAt,
    now,
  );
  const updatedAt = now;

  return {
    id: scenarioId,
    name: normalizeDisplayName(record, scenarioId),
    createdAt,
    updatedAt,
    stepCount: bundle.steps.length,
    annotationCount: bundle.annotations.length,
    bundle,
  };
}

function normalizeStoredScenarioRecord(record, now) {
  if (!isPlainObject(record)) {
    return null;
  }

  try {
    const bundle = normalizeScenarioBundle(record);
    const scenarioId = bundle.metadata.scenarioId;
    const createdAt = normalizeTimestamp(record.createdAt ?? bundle.metadata.createdAt, now);
    const updatedAt = normalizeTimestamp(
      record.updatedAt ?? record.createdAt ?? bundle.metadata.updatedAt,
      createdAt,
    );

    return {
      id: scenarioId,
      name: normalizeDisplayName(record, scenarioId),
      createdAt,
      updatedAt,
      stepCount: bundle.steps.length,
      annotationCount: bundle.annotations.length,
      bundle,
    };
  } catch {
    return null;
  }
}

function partitionStoredScenarios(records, now) {
  const readable = [];
  const unreadable = [];

  for (const record of records) {
    const clonedRecord = cloneValue(record);
    const normalizedRecord = normalizeStoredScenarioRecord(clonedRecord, now);
    if (normalizedRecord) {
      readable.push(normalizedRecord);
    } else {
      unreadable.push(clonedRecord);
    }
  }

  return { readable, unreadable };
}

function getScenarioIdFromInput(input) {
  if (isPlainObject(input?.bundle) && isNonEmptyString(input.bundle?.metadata?.scenarioId)) {
    return input.bundle.metadata.scenarioId.trim();
  }

  if (isPlainObject(input?.metadata) && isNonEmptyString(input.metadata.scenarioId)) {
    return input.metadata.scenarioId.trim();
  }

  if (isNonEmptyString(input?.id)) {
    return input.id.trim();
  }

  return null;
}

function sortScenariosByUpdatedAtDescending(scenarios) {
  return [...scenarios].sort((left, right) => {
    if (left.updatedAt === right.updatedAt) {
      return left.id.localeCompare(right.id);
    }

    return right.updatedAt.localeCompare(left.updatedAt);
  });
}

function createChromeStorageAdapter() {
  if (!globalThis.chrome?.storage?.local) {
    throw new Error('chrome.storage.local is unavailable');
  }

  return {
    async readScenarios() {
      return new Promise((resolve, reject) => {
        chrome.storage.local.get(STORAGE_KEY, (items) => {
          const error = chrome.runtime?.lastError;
          if (error) {
            reject(new Error(error.message));
            return;
          }

          const scenarios = Array.isArray(items?.[STORAGE_KEY]) ? items[STORAGE_KEY] : [];
          resolve(cloneValue(scenarios));
        });
      });
    },
    async writeScenarios(scenarios) {
      return new Promise((resolve, reject) => {
        chrome.storage.local.set({ [STORAGE_KEY]: cloneValue(scenarios) }, () => {
          const error = chrome.runtime?.lastError;
          if (error) {
            reject(new Error(error.message));
            return;
          }

          resolve();
        });
      });
    },
  };
}

export function createScenarioStore(adapter = createChromeStorageAdapter(), options = {}) {
  const now = options.now ?? (() => new Date().toISOString());
  let mutationQueue = Promise.resolve();

  async function readScenarios() {
    const scenarios = await adapter.readScenarios();
    if (!Array.isArray(scenarios)) {
      return [];
    }

    return partitionStoredScenarios(scenarios, new Date().toISOString()).readable;
  }

  async function writeScenarios(scenarios) {
    await adapter.writeScenarios(scenarios);
  }

  function runExclusive(operation) {
    const next = mutationQueue.then(operation, operation);
    mutationQueue = next.catch(() => {});
    return next;
  }

  return {
    async listScenarios() {
      return sortScenariosByUpdatedAtDescending(await readScenarios());
    },

    async saveScenario(input) {
      return runExclusive(async () => {
        const rawScenarios = await adapter.readScenarios();
        const { readable, unreadable } = Array.isArray(rawScenarios)
          ? partitionStoredScenarios(rawScenarios, new Date().toISOString())
          : { readable: [], unreadable: [] };
        const nextNow = now();
        const scenarioId = getScenarioIdFromInput(input);
        const index = readable.findIndex((scenario) => scenario.id === scenarioId);
        const existingRecord = index >= 0 ? readable[index] : null;
        const record = normalizeScenarioRecord(input, existingRecord, nextNow);

        if (index >= 0) {
          readable[index] = record;
        } else {
          readable.push(record);
        }

        await writeScenarios([...unreadable, ...readable]);
        return cloneValue(record);
      });
    },

    async deleteScenario(id) {
      return runExclusive(async () => {
        if (!isNonEmptyString(id)) {
          throw new Error('Scenario id must be a non-empty string');
        }

        const rawScenarios = await adapter.readScenarios();
        const { readable, unreadable } = Array.isArray(rawScenarios)
          ? partitionStoredScenarios(rawScenarios, new Date().toISOString())
          : { readable: [], unreadable: [] };
        const index = readable.findIndex((scenario) => scenario.id === id.trim());
        if (index < 0) {
          return false;
        }

        readable.splice(index, 1);
        await writeScenarios([...unreadable, ...readable]);
        return true;
      });
    },
  };
}

export { STORAGE_KEY, createChromeStorageAdapter };
