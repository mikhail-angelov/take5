import { createReplayPlan } from './plan-generator.js';

function cloneValue(value) {
  if (typeof structuredClone === 'function') {
    return structuredClone(value);
  }

  return JSON.parse(JSON.stringify(value));
}

function waitForDelay(delay, step) {
  if (typeof delay !== 'function') {
    return Promise.resolve();
  }

  return delay(step);
}

function collectAnnotations(plan) {
  const annotations = [...(plan.annotations ?? [])];

  for (const step of plan.steps ?? []) {
    if (Array.isArray(step.annotations)) {
      annotations.push(...step.annotations);
    }
  }

  return annotations;
}

export function createReplayEngine(options = {}) {
  const overlay = options.overlay ?? null;
  const runStep = options.runStep ?? (async () => {});
  const delay = options.delay ?? (async () => {});

  const state = {
    mode: 'idle',
    index: 0,
    plan: null,
    running: false,
  };

  function getState() {
    return cloneValue({
      mode: state.mode,
      index: state.index,
      running: state.running,
      total: state.plan?.steps?.length ?? 0,
    });
  }

  async function renderPlan() {
    if (!state.plan) {
      return;
    }

    overlay?.clear?.();
    overlay?.setReplayMode?.(state.mode);
    overlay?.showAnnotations?.(collectAnnotations(state.plan));
  }

  async function load(bundle) {
    state.plan = createReplayPlan(bundle);
    state.index = 0;
    state.mode = 'step';
    state.running = false;
    await renderPlan();
    return getState();
  }

  async function executeStep(step) {
    overlay?.showStep?.(step);
    await runStep(step, {
      plan: state.plan,
      index: state.index,
    });
    await waitForDelay(delay, step);
  }

  async function play() {
    if (!state.plan) {
      throw new Error('Replay plan has not been loaded');
    }

    state.mode = 'auto';
    state.running = true;
    overlay?.setReplayMode?.('auto');

    while (state.index < state.plan.steps.length) {
      const step = state.plan.steps[state.index];
      await executeStep(step);
      state.index += 1;
    }

    state.mode = 'idle';
    state.running = false;
    overlay?.setReplayMode?.('idle');
    return getState();
  }

  async function next() {
    if (!state.plan) {
      throw new Error('Replay plan has not been loaded');
    }

    state.mode = 'step';
    overlay?.setReplayMode?.('step');

    if (state.index >= state.plan.steps.length) {
      return getState();
    }

    const step = state.plan.steps[state.index];
    await executeStep(step);
    state.index += 1;
    return getState();
  }

  async function back() {
    if (!state.plan) {
      throw new Error('Replay plan has not been loaded');
    }

    state.mode = 'step';
    overlay?.setReplayMode?.('step');

    if (state.index > 0) {
      state.index -= 1;
    }

    const step = state.plan.steps[state.index] ?? null;
    if (step) {
      overlay?.showStep?.(step);
    }

    return getState();
  }

  async function stop() {
    state.mode = 'idle';
    state.running = false;
    overlay?.setReplayMode?.('idle');
    return getState();
  }

  return {
    load,
    play,
    next,
    back,
    stop,
    getState,
    getPlan() {
      return state.plan ? cloneValue(state.plan) : null;
    },
  };
}
