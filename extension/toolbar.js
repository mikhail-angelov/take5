import { buildScenarioExportFilename, parseScenarioJson, serializeScenario } from './schema-export.js';

const TOGGLE_MESSAGE_TYPE = 'take5:toolbar-toggle';
const COMMAND_MESSAGE_TYPE = 'take5:command';

function createEmptyBundle() {
  return {
    steps: [],
    annotations: [],
  };
}

function createConfirmDiscardChanges(confirmImplementation) {
  if (typeof confirmImplementation === 'function') {
    return confirmImplementation;
  }

  if (typeof globalThis.confirm === 'function') {
    return globalThis.confirm.bind(globalThis);
  }

  return () => true;
}

export function createToolbarController(options = {}) {
  const confirmDiscardChanges = createConfirmDiscardChanges(options.confirmDiscardChanges);
  const state = {
    scenarios: [],
    selectedScenarioId: '',
    editorValue: '',
    editorDirty: false,
  };

  function getSelectedScenario() {
    return state.scenarios.find((scenario) => scenario.id === state.selectedScenarioId) ?? null;
  }

  function replaceEditorFromSelection() {
    const selected = getSelectedScenario();
    state.editorValue = selected ? serializeScenario(selected.bundle) : '';
    state.editorDirty = false;
  }

  function confirmEditorReset(reason) {
    if (!state.editorDirty) {
      return true;
    }

    return confirmDiscardChanges(
      `Discard unsaved scenario JSON before ${reason}?`,
    );
  }

  function applyScenarioSelection(nextScenarioId, options = {}) {
    const { force = false } = options;
    if (!force && !confirmEditorReset('switching scenarios')) {
      return false;
    }

    state.selectedScenarioId = nextScenarioId;
    replaceEditorFromSelection();
    return true;
  }

  return {
    getScenarios() {
      return structuredClone(state.scenarios);
    },

    getSelectedScenarioId() {
      return state.selectedScenarioId;
    },

    getSelectedScenario,

    getEditorValue() {
      return state.editorValue;
    },

    isEditorDirty() {
      return state.editorDirty;
    },

    setEditorValue(value, options = {}) {
      state.editorValue = value;
      state.editorDirty = options.markDirty ?? true;
    },

    setScenarios(scenarios) {
      state.scenarios = structuredClone(scenarios);
      if (!state.scenarios.some((scenario) => scenario.id === state.selectedScenarioId)) {
        state.selectedScenarioId = state.scenarios[0]?.id ?? '';
      }
    },

    selectScenario(nextScenarioId, options = {}) {
      return applyScenarioSelection(nextScenarioId, options);
    },

    refreshScenarios(nextScenarios, options = {}) {
      const { preserveSelection = true, force = false } = options;
      if (!force && !confirmEditorReset('refreshing scenarios')) {
        return false;
      }

      const selectedId = preserveSelection ? state.selectedScenarioId : '';
      state.scenarios = structuredClone(nextScenarios);
      state.selectedScenarioId = state.scenarios.some((scenario) => scenario.id === selectedId)
        ? selectedId
        : state.scenarios[0]?.id ?? '';
      replaceEditorFromSelection();
      return true;
    },

    syncEditorFromSelection() {
      replaceEditorFromSelection();
    },

    applySavedScenario(savedScenario, nextScenarios) {
      state.scenarios = structuredClone(nextScenarios);
      state.selectedScenarioId = savedScenario.id;
      replaceEditorFromSelection();
    },

    getCurrentBundle() {
      return getSelectedScenario()?.bundle ?? createEmptyBundle();
    },
  };
}

const controller = createToolbarController();
let activeCaptureBundle = null;

const hasDocument = typeof document !== 'undefined';

const elements = hasDocument
  ? {
      statusPill: document.getElementById('status-pill'),
      stepCount: document.getElementById('step-count'),
      annotationCount: document.getElementById('annotation-count'),
      scenarioCount: document.getElementById('scenario-count'),
      scenarioList: document.getElementById('scenario-list'),
      scenarioJson: document.getElementById('scenario-json'),
      feedback: document.getElementById('feedback'),
      dismissButton: document.getElementById('dismiss-button'),
      refreshButton: document.getElementById('refresh-button'),
      importButton: document.getElementById('import-button'),
      exportButton: document.getElementById('export-button'),
      importInput: document.getElementById('import-input'),
    }
  : null;

function setFeedback(message, isError = false) {
  elements.feedback.textContent = message;
  elements.feedback.classList.toggle('danger', isError);
}

function setMode(mode) {
  const label = mode === 'capturing' ? 'Capturing' : mode === 'replaying' ? 'Replaying' : 'Idle';
  elements.statusPill.textContent = label;
}

function sendCommand(command, payload = {}) {
  window.parent.postMessage(
    {
      type: COMMAND_MESSAGE_TYPE,
      command,
      payload,
    },
    '*',
  );
}

function renderStats() {
  const bundle = activeCaptureBundle ?? controller.getCurrentBundle();

  elements.stepCount.textContent = String(bundle.steps?.length ?? 0);
  elements.annotationCount.textContent = String(bundle.annotations?.length ?? 0);
  elements.scenarioCount.textContent = String(controller.getScenarios().length);
}

function renderScenarioList() {
  elements.scenarioList.innerHTML = '';

  for (const scenario of controller.getScenarios()) {
    const option = document.createElement('option');
    option.value = scenario.id;
    option.textContent = `${scenario.name} · ${scenario.stepCount} steps`;
    if (scenario.id === controller.getSelectedScenarioId()) {
      option.selected = true;
    }
    elements.scenarioList.appendChild(option);
  }

  elements.scenarioList.value = controller.getSelectedScenarioId();
  renderStats();
}

function renderScenarioJson() {
  elements.scenarioJson.value = controller.getEditorValue();
}

function renderAll() {
  renderScenarioList();
  renderScenarioJson();
}

async function sendMessage(type, payload = {}) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage({ type, payload }, (response) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }

      if (!response?.ok) {
        reject(new Error(response?.error || 'Unknown extension error'));
        return;
      }

      resolve(response);
    });
  });
}

async function refreshScenarios(options = {}) {
  const response = await sendMessage('take5:list-scenarios');
  const updated = controller.refreshScenarios(response.scenarios ?? [], options);
  if (!updated) {
    return false;
  }

  renderAll();
  return true;
}

function getReplayBundleFromEditor() {
  if (!elements.scenarioJson.value.trim()) {
    return controller.getCurrentBundle();
  }

  return parseScenarioJson(elements.scenarioJson.value);
}

async function saveScenarioFromEditor() {
  const bundle = parseScenarioJson(elements.scenarioJson.value);
  const response = await sendMessage('take5:save-scenario', bundle);
  const savedScenario = response.scenario;
  const listResponse = await sendMessage('take5:list-scenarios');
  activeCaptureBundle = null;
  controller.applySavedScenario(savedScenario, listResponse.scenarios ?? []);
  renderAll();
  setFeedback(`Saved ${savedScenario.name}`);
}

async function deleteSelectedScenario() {
  const selected = controller.getSelectedScenario();
  if (!selected) {
    setFeedback('Pick a scenario to delete first.', true);
    return;
  }

  await sendMessage('take5:delete-scenario', { id: selected.id });
  const refreshed = await refreshScenarios({
    preserveSelection: false,
    force: true,
  });
  if (refreshed) {
    setFeedback(`Deleted ${selected.name}`);
  }
}

async function exportSelectedScenario() {
  const selected = controller.getSelectedScenario();
  if (!selected) {
    setFeedback('Pick a scenario to export first.', true);
    return;
  }

  const json = serializeScenario(selected.bundle);
  const blob = new Blob([json], { type: 'application/json' });
  const url = URL.createObjectURL(blob);

  try {
    await chrome.downloads.download({
      url,
      filename: buildScenarioExportFilename(selected),
      saveAs: true,
    });
    setFeedback(`Exported ${selected.name}`);
  } finally {
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
}

async function importScenarioFile(file) {
  const json = await file.text();
  elements.scenarioJson.value = json;
  controller.setEditorValue(json);
  const bundle = parseScenarioJson(json);
  const response = await sendMessage('take5:save-scenario', bundle);
  const listResponse = await sendMessage('take5:list-scenarios');
  activeCaptureBundle = null;
  controller.applySavedScenario(response.scenario, listResponse.scenarios ?? []);
  renderAll();
  setFeedback(`Imported ${response.scenario.name}`);
}

function wireActions() {
  elements.dismissButton.addEventListener('click', () => {
    window.parent.postMessage({ type: TOGGLE_MESSAGE_TYPE }, '*');
  });

  document.querySelector('[data-action="start"]').addEventListener('click', () => {
    sendCommand('capture:start', {
      scenarioId: controller.getSelectedScenario()?.id ?? '',
      name: controller.getSelectedScenario()?.name ?? document.title,
      baseUrl: window.location.href,
    });
  });

  document.querySelector('[data-action="stop"]').addEventListener('click', () => {
    sendCommand('capture:stop');
  });

  document.querySelector('[data-action="save"]').addEventListener('click', async () => {
    try {
      await saveScenarioFromEditor();
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  document.querySelector('[data-action="replay"]').addEventListener('click', (event) => {
    try {
      sendCommand('replay:start', {
        mode: event.shiftKey ? 'step' : 'auto',
        bundle: getReplayBundleFromEditor(),
      });
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  document.querySelector('[data-action="scenarios"]').addEventListener('click', async () => {
    try {
      const refreshed = await refreshScenarios();
      if (!refreshed) {
        setFeedback('Kept unsaved scenario JSON.', true);
        return;
      }
      setFeedback('Scenario list refreshed.');
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  document.querySelector('[data-action="delete"]').addEventListener('click', async () => {
    try {
      await deleteSelectedScenario();
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  elements.refreshButton.addEventListener('click', async () => {
    try {
      const refreshed = await refreshScenarios();
      if (!refreshed) {
        setFeedback('Kept unsaved scenario JSON.', true);
        return;
      }
      setFeedback('Scenario list refreshed.');
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  elements.importButton.addEventListener('click', () => {
    elements.importInput.click();
  });

  elements.exportButton.addEventListener('click', async () => {
    try {
      await exportSelectedScenario();
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  elements.importInput.addEventListener('change', async () => {
    const file = elements.importInput.files?.[0];
    elements.importInput.value = '';
    if (!file) {
      return;
    }

    try {
      await importScenarioFile(file);
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  elements.scenarioJson.addEventListener('input', () => {
    controller.setEditorValue(elements.scenarioJson.value);
  });

  elements.scenarioList.addEventListener('change', () => {
    const changed = controller.selectScenario(elements.scenarioList.value);
    if (!changed) {
      elements.scenarioList.value = controller.getSelectedScenarioId();
      setFeedback('Kept unsaved scenario JSON.', true);
      return;
    }

    renderScenarioJson();
    renderStats();
  });

  window.addEventListener('message', (event) => {
    if (event.source !== window.parent) {
      return;
    }

    const message = event.data;
    if (!message?.type) {
      return;
    }

    if (message.type === 'take5:status') {
      setMode(message.mode);
      setFeedback(message.message ?? '');
      return;
    }

    if (message.type === 'take5:capture-state' && message.bundle) {
      activeCaptureBundle = message.bundle;
      controller.setEditorValue(serializeScenario(message.bundle), { markDirty: false });
      renderScenarioJson();
      renderStats();
      setMode('capturing');
      setFeedback('Capture bundle loaded. Save it when you are ready.');
    }
  });
}

async function init() {
  wireActions();
  setMode('idle');
  setFeedback('Ready.');
  await refreshScenarios({
    preserveSelection: false,
    force: true,
  });
}

if (hasDocument) {
  init().catch((error) => {
    setFeedback(error.message, true);
  });
}
