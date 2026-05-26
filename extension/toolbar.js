import { parseScenarioJson, serializeScenario } from './schema-export.js';

const state = {
  mode: 'idle',
  scenarios: [],
  selectedScenarioId: '',
};

const elements = {
  statusPill: document.getElementById('status-pill'),
  stepCount: document.getElementById('step-count'),
  annotationCount: document.getElementById('annotation-count'),
  scenarioCount: document.getElementById('scenario-count'),
  scenarioList: document.getElementById('scenario-list'),
  scenarioJson: document.getElementById('scenario-json'),
  feedback: document.getElementById('feedback'),
  refreshButton: document.getElementById('refresh-button'),
  importButton: document.getElementById('import-button'),
  exportButton: document.getElementById('export-button'),
  importInput: document.getElementById('import-input'),
};

function setFeedback(message, isError = false) {
  elements.feedback.textContent = message;
  elements.feedback.classList.toggle('danger', isError);
}

function setMode(mode) {
  state.mode = mode;
  const label = mode === 'capturing' ? 'Capturing' : mode === 'replaying' ? 'Replaying' : 'Idle';
  elements.statusPill.textContent = label;
}

function getSelectedScenario() {
  return state.scenarios.find((scenario) => scenario.id === state.selectedScenarioId) ?? null;
}

function renderStats() {
  const selected = getSelectedScenario();
  const scenario = selected ?? state.scenarios[0] ?? null;
  const bundle = scenario?.bundle ?? { steps: [], annotations: [] };

  elements.stepCount.textContent = String(bundle.steps?.length ?? 0);
  elements.annotationCount.textContent = String(bundle.annotations?.length ?? 0);
  elements.scenarioCount.textContent = String(state.scenarios.length);
}

function renderScenarioList() {
  elements.scenarioList.innerHTML = '';

  for (const scenario of state.scenarios) {
    const option = document.createElement('option');
    option.value = scenario.id;
    option.textContent = `${scenario.name} · ${scenario.stepCount} steps`;
    if (scenario.id === state.selectedScenarioId) {
      option.selected = true;
    }
    elements.scenarioList.appendChild(option);
  }

  if (!state.selectedScenarioId && state.scenarios.length > 0) {
    state.selectedScenarioId = state.scenarios[0].id;
    elements.scenarioList.value = state.selectedScenarioId;
  }

  renderStats();
}

function renderScenarioJson() {
  const selected = getSelectedScenario();
  elements.scenarioJson.value = selected ? serializeScenario(selected.bundle) : '';
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

async function refreshScenarios({ preserveSelection = true } = {}) {
  const selectedId = preserveSelection ? state.selectedScenarioId : '';
  const response = await sendMessage('take5:list-scenarios');
  state.scenarios = response.scenarios ?? [];
  state.selectedScenarioId = state.scenarios.some((scenario) => scenario.id === selectedId)
    ? selectedId
    : state.scenarios[0]?.id ?? '';
  renderScenarioList();
  renderScenarioJson();
}

async function saveScenarioFromEditor() {
  const bundle = parseScenarioJson(elements.scenarioJson.value);
  const response = await sendMessage('take5:save-scenario', bundle);
  const savedScenario = response.scenario;
  setFeedback(`Saved ${savedScenario.name}`);
  await refreshScenarios({ preserveSelection: false });
  state.selectedScenarioId = savedScenario.id;
  elements.scenarioList.value = savedScenario.id;
  renderScenarioJson();
}

async function deleteSelectedScenario() {
  const selected = getSelectedScenario();
  if (!selected) {
    setFeedback('Pick a scenario to delete first.', true);
    return;
  }

  await sendMessage('take5:delete-scenario', { id: selected.id });
  setFeedback(`Deleted ${selected.name}`);
  state.selectedScenarioId = '';
  await refreshScenarios({ preserveSelection: false });
}

async function exportSelectedScenario() {
  const selected = getSelectedScenario();
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
      filename: `take5-${selected.id}.json`,
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
  const bundle = parseScenarioJson(json);
  const response = await sendMessage('take5:save-scenario', bundle);
  setFeedback(`Imported ${response.scenario.name}`);
  await refreshScenarios({ preserveSelection: false });
  state.selectedScenarioId = response.scenario.id;
  elements.scenarioList.value = response.scenario.id;
  renderScenarioJson();
}

function wireActions() {
  document.querySelector('[data-action="start"]').addEventListener('click', () => {
    setMode('capturing');
    setFeedback('Capture shell armed. Scenario recording is not wired yet.');
  });

  document.querySelector('[data-action="stop"]').addEventListener('click', () => {
    setMode('idle');
    setFeedback('Capture stopped.');
  });

  document.querySelector('[data-action="save"]').addEventListener('click', async () => {
    try {
      await saveScenarioFromEditor();
    } catch (error) {
      setFeedback(error.message, true);
    }
  });

  document.querySelector('[data-action="replay"]').addEventListener('click', () => {
    setMode('replaying');
    setFeedback('Replay shell is present, but the replay engine is not implemented yet.');
  });

  document.querySelector('[data-action="scenarios"]').addEventListener('click', async () => {
    try {
      await refreshScenarios();
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
      await refreshScenarios();
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

  elements.scenarioList.addEventListener('change', () => {
    state.selectedScenarioId = elements.scenarioList.value;
    renderScenarioJson();
    renderStats();
  });
}

async function init() {
  wireActions();
  setMode('idle');
  setFeedback('Ready.');
  await refreshScenarios({ preserveSelection: false });
}

init().catch((error) => {
  setFeedback(error.message, true);
});
