import {
  buildScenarioExportFilename,
  parseScenarioJson,
  serializeScenario,
} from './schema-export.js';

const TOGGLE_MESSAGE_TYPE = 'take5:toolbar-toggle';
const RESIZE_MESSAGE_TYPE = 'take5:toolbar-resize';
const DRAG_START_MESSAGE_TYPE = 'take5:toolbar-drag-start';
const COMMAND_MESSAGE_TYPE = 'take5:command';

function log(...args) {
  try {
    console.log('[Take5:toolbar]', ...args);
  } catch {
    // console may be unavailable in some contexts.
  }
}

function createEmptyBundle() {
  return {
    steps: [],
    annotations: [],
  };
}

export function buildCaptureStartPayload(selectedScenario) {
  const payload = {};

  if (selectedScenario?.id) {
    payload.scenarioId = selectedScenario.id;
  }

  if (selectedScenario?.name) {
    payload.name = selectedScenario.name;
  }

  return payload;
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

    return confirmDiscardChanges(`Discard unsaved scenario JSON before ${reason}?`);
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
        : (state.scenarios[0]?.id ?? '');
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
      panel: document.getElementById('panel'),
      statusPill: document.getElementById('status-pill'),
      barStepCount: document.getElementById('bar-step-count'),
      stepCount: document.getElementById('step-count'),
      annotationCount: document.getElementById('annotation-count'),
      scenarioCount: document.getElementById('scenario-count'),
      scenarioList: document.getElementById('scenario-list'),
      scenarioJson: document.getElementById('scenario-json'),
      feedback: document.getElementById('feedback'),
      dismissButton: document.getElementById('dismiss-button'),
      expandButton: document.getElementById('expand-button'),
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
  log('sendCommand ->', command);
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

  const stepCount = String(bundle.steps?.length ?? 0);
  elements.stepCount.textContent = stepCount;
  elements.barStepCount.textContent = stepCount;
  elements.annotationCount.textContent = String(bundle.annotations?.length ?? 0);
  elements.scenarioCount.textContent = String(controller.getScenarios().length);
}

// Natural height of the top bar, so the collapsed frame can wrap it tightly
// instead of leaving dead space below.
function measureBarHeight() {
  const bar = document.querySelector('.bar');
  if (!bar) {
    return undefined;
  }
  const height = Math.ceil(bar.getBoundingClientRect().height);
  return height > 0 ? height : undefined;
}

function setExpanded(expanded) {
  elements.panel.dataset.expanded = expanded ? 'true' : 'false';
  elements.expandButton.setAttribute('aria-expanded', expanded ? 'true' : 'false');
  elements.expandButton.textContent = expanded ? 'Collapse' : 'Expand';
  window.parent.postMessage(
    { type: RESIZE_MESSAGE_TYPE, expanded, height: expanded ? undefined : measureBarHeight() },
    '*',
  );
}

// The bar acts as a drag handle: on mousedown we hand the drag to the parent
// page, which can track the pointer across the whole viewport (the iframe only
// receives events over its own, shrinking area).
function wireDragHandle() {
  const bar = document.querySelector('.bar');
  if (!bar) {
    return;
  }

  bar.addEventListener('mousedown', (event) => {
    if (event.button !== 0 || event.target.closest('button, select, input, textarea, a')) {
      return;
    }
    event.preventDefault();
    window.parent.postMessage(
      { type: DRAG_START_MESSAGE_TYPE, x: event.clientX, y: event.clientY },
      '*',
    );
  });
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

function resolveExportBundle() {
  if (elements.scenarioJson.value.trim()) {
    return parseScenarioJson(elements.scenarioJson.value);
  }

  const selected = controller.getSelectedScenario();
  return selected ? selected.bundle : null;
}

async function exportCurrentScenario() {
  const bundle = resolveExportBundle();
  if (!bundle || !(bundle.steps?.length > 0)) {
    setFeedback('Nothing to export yet. Record or pick a scenario first.', true);
    return;
  }

  const json = serializeScenario(bundle);
  const blob = new Blob([json], { type: 'application/json' });
  const url = URL.createObjectURL(blob);

  try {
    await chrome.downloads.download({
      url,
      filename: buildScenarioExportFilename(bundle),
      saveAs: true,
    });
    setFeedback(`Exported ${bundle.metadata?.name ?? 'scenario'}`);
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

  elements.expandButton.addEventListener('click', () => {
    setExpanded(elements.panel.dataset.expanded !== 'true');
  });

  document.querySelector('[data-action="start"]').addEventListener('click', () => {
    sendCommand('capture:start', buildCaptureStartPayload(controller.getSelectedScenario()));
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
      await exportCurrentScenario();
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
      log('capture-state received; steps =', message.bundle.steps?.length);
      activeCaptureBundle = message.bundle;
      controller.setEditorValue(serializeScenario(message.bundle), { markDirty: false });
      renderScenarioJson();
      renderStats();
      // Mode/feedback are owned by take5:status messages so a final capture-state
      // after Stop does not flip the indicator back to "Capturing".
    }
  });
}

async function init() {
  wireActions();
  wireDragHandle();
  setMode('idle');
  setFeedback('Ready.');
  // Tighten the collapsed frame to the bar's real height once laid out.
  requestAnimationFrame(() => {
    const height = measureBarHeight();
    if (height) {
      window.parent.postMessage({ type: RESIZE_MESSAGE_TYPE, expanded: false, height }, '*');
    }
  });
  // Ask the content script for any capture already in progress (e.g. resumed
  // after a navigation) so the panel reflects it immediately.
  window.parent.postMessage({ type: 'take5:request-state' }, '*');
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
