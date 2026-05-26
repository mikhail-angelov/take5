import { validateCaptureBundle } from '../src/capture-schema.js';

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

function toStepId(index) {
  return `step-${index + 1}`;
}

function getTimestamp(now) {
  const value = now();
  return isNonEmptyString(value) ? value : new Date().toISOString();
}

function resolveElementSelector(element) {
  if (!element || typeof element !== 'object') {
    return null;
  }

  if (isNonEmptyString(element.selector)) {
    return element.selector.trim();
  }

  if (isNonEmptyString(element.dataset?.take5Selector)) {
    return element.dataset.take5Selector.trim();
  }

  if (isNonEmptyString(element.id)) {
    return `#${element.id.trim()}`;
  }

  if (isNonEmptyString(element.getAttribute?.('name'))) {
    return `${String(element.tagName || 'input').toLowerCase()}[name="${element.getAttribute('name')}"]`;
  }

  if (isNonEmptyString(element.getAttribute?.('aria-label'))) {
    return `${String(element.tagName || 'button').toLowerCase()}[aria-label="${element.getAttribute('aria-label')}"]`;
  }

  return null;
}

function readElementRect(element) {
  if (!element || typeof element.getBoundingClientRect !== 'function') {
    return null;
  }

  const rect = element.getBoundingClientRect();
  if (!rect) {
    return null;
  }

  return {
    x: rect.x,
    y: rect.y,
    width: rect.width,
    height: rect.height,
  };
}

function readTargetValue(target) {
  if (!target || typeof target !== 'object') {
    return '';
  }

  if (typeof target.value === 'string') {
    return target.value;
  }

  return '';
}

function isFormField(target) {
  const tagName = String(target?.tagName || '').toLowerCase();
  return tagName === 'input' || tagName === 'textarea';
}

function isSelectField(target) {
  return String(target?.tagName || '').toLowerCase() === 'select';
}

function isNavigationKey(key) {
  return ['Enter', 'Tab', 'Escape', 'Backspace', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(key);
}

function createInitialBundle(options, now) {
  const startedAt = getTimestamp(now);
  const scenarioId =
    options.scenarioId ??
    `capture-${startedAt.replace(/[:.]/g, '-')}-${Math.random().toString(16).slice(2, 8)}`;

  return {
    metadata: {
      schemaVersion: 1,
      scenarioId,
      name: options.name ?? options.title ?? scenarioId,
      baseUrl: options.baseUrl ?? null,
      viewportPreset: options.viewportPreset ?? 'desktop-1280',
      extensionVersion: options.extensionVersion ?? '0.1.0',
      startedAt,
      updatedAt: startedAt,
    },
    steps: [],
    annotations: [],
    debug: {
      pageTitle: options.document?.title ?? '',
      startedAt,
      events: [],
    },
  };
}

export function createCaptureEngine(options = {}) {
  const document = options.document ?? globalThis.document;
  const windowObject = options.window ?? globalThis.window;
  const overlay = options.overlay;
  const now = options.now ?? (() => new Date().toISOString());
  const promptForAnnotation = options.promptForAnnotation ?? overlay?.promptForAnnotation?.bind(overlay);
  const listeners = [];

  const state = {
    bundle: null,
    capturing: false,
    annotationMode: false,
    lastScrollX: 0,
    lastScrollY: 0,
  };

  function recordStep(step) {
    if (!state.bundle) {
      return null;
    }

    const nextStep = {
      ...step,
      id: step.id ?? toStepId(state.bundle.steps.length),
    };
    state.bundle.steps.push(nextStep);
    state.bundle.metadata.updatedAt = getTimestamp(now);
    return nextStep;
  }

  function getBundle() {
    if (!state.bundle) {
      throw new Error('Capture has not started');
    }

    return cloneValue(validateCaptureBundle(cloneValue(state.bundle)));
  }

  function appendDebugEvent(entry) {
    if (!state.bundle) {
      return;
    }

    state.bundle.debug.events.push({
      ...entry,
      at: getTimestamp(now),
    });
  }

  function recordAnnotation(target, description) {
    if (!state.bundle) {
      return null;
    }

    const selector = resolveElementSelector(target);
    const annotation = {
      description: description.trim(),
      pageUrl: windowObject?.location?.href ?? document?.location?.href ?? state.bundle.metadata.baseUrl ?? '',
      createdAt: getTimestamp(now),
      orderIndex: state.bundle.annotations.length,
      stepIndex: Math.max(0, state.bundle.steps.length - 1),
      nearestStepId: state.bundle.steps.at(-1)?.id ?? null,
    };

    if (selector) {
      annotation.selector = selector;
    }

    const rect = readElementRect(target);
    if (rect) {
      annotation.targetRect = rect;
    }

    state.bundle.annotations.push(annotation);
    overlay?.pinAnnotation?.(annotation);
    appendDebugEvent({ type: 'annotation', selector: annotation.selector ?? null });
    return annotation;
  }

  async function maybePromptAnnotation(target) {
    if (typeof promptForAnnotation !== 'function') {
      return null;
    }

    const description = await promptForAnnotation(target, {
      defaultValue: '',
    });

    if (!isNonEmptyString(description)) {
      return null;
    }

    return recordAnnotation(target, description);
  }

  function handleKeydown(event) {
    if (!state.capturing) {
      return;
    }

    appendDebugEvent({ type: 'keydown', key: event.key });

    if (event.key === 'Control') {
      state.annotationMode = true;
      overlay?.setAnnotationMode?.(true);
      return;
    }

    if (isNavigationKey(event.key)) {
      recordStep({
        type: 'keypress',
        key: event.key,
      });
    }
  }

  function handleKeyup(event) {
    if (!state.capturing) {
      return;
    }

    appendDebugEvent({ type: 'keyup', key: event.key });

    if (event.key === 'Control') {
      state.annotationMode = false;
      overlay?.setAnnotationMode?.(false);
    }
  }

  function handleClick(event) {
    if (!state.capturing) {
      return;
    }

    const target = event.target ?? event.currentTarget ?? null;
    appendDebugEvent({ type: 'click', selector: resolveElementSelector(target) });

    if (state.annotationMode || event.ctrlKey) {
      event.preventDefault?.();
      event.stopPropagation?.();
      event.stopImmediatePropagation?.();
      maybePromptAnnotation(target).catch(() => {});
      return;
    }

    recordStep({
      type: 'click',
      selector: resolveElementSelector(target) ?? undefined,
      ref: target?.dataset?.take5Ref ?? undefined,
    });
  }

  function handleInput(event) {
    if (!state.capturing) {
      return;
    }

    const target = event.target ?? null;
    const selector = resolveElementSelector(target);
    if (selector && isFormField(target)) {
      appendDebugEvent({ type: 'input', selector });
    }
  }

  function handleChange(event) {
    if (!state.capturing) {
      return;
    }

    const target = event.target ?? null;
    const selector = resolveElementSelector(target);
    if (!selector) {
      return;
    }

    if (isSelectField(target)) {
      recordStep({
        type: 'select',
        selector,
        value: readTargetValue(target),
      });
      return;
    }

    if (isFormField(target)) {
      recordStep({
        type: 'fill',
        selector,
        value: readTargetValue(target),
      });
    }
  }

  function handleScroll() {
    if (!state.capturing) {
      return;
    }

    const nextScrollX = windowObject?.scrollX ?? document?.defaultView?.scrollX ?? 0;
    const nextScrollY = windowObject?.scrollY ?? document?.defaultView?.scrollY ?? 0;
    const deltaX = nextScrollX - state.lastScrollX;
    const deltaY = nextScrollY - state.lastScrollY;

    if (deltaX === 0 && deltaY === 0) {
      return;
    }

    state.lastScrollX = nextScrollX;
    state.lastScrollY = nextScrollY;

    if (deltaY !== 0) {
      recordStep({
        type: 'scroll',
        direction: deltaY > 0 ? 'down' : 'up',
        amount: Math.abs(deltaY),
      });
    }
  }

  function handlePopstate() {
    if (!state.capturing) {
      return;
    }

    const url = windowObject?.location?.href ?? document?.location?.href;
    if (!url || state.bundle.steps.at(-1)?.url === url) {
      return;
    }

    recordStep({
      type: 'navigate',
      url,
    });
  }

  function wireListeners() {
    if (!document?.addEventListener) {
      return;
    }

    const listenerPairs = [
      ['keydown', handleKeydown, true],
      ['keyup', handleKeyup, true],
      ['click', handleClick, true],
      ['input', handleInput, true],
      ['change', handleChange, true],
      ['scroll', handleScroll, true],
    ];

    for (const [type, handler, useCapture] of listenerPairs) {
      document.addEventListener(type, handler, useCapture);
      listeners.push([type, handler, useCapture]);
    }

    windowObject?.addEventListener?.('popstate', handlePopstate, true);
    listeners.push(['popstate', handlePopstate, true, windowObject]);
    windowObject?.addEventListener?.('hashchange', handlePopstate, true);
    listeners.push(['hashchange', handlePopstate, true, windowObject]);
  }

  function unwireListeners() {
    if (!document?.removeEventListener) {
      listeners.length = 0;
      return;
    }

    for (const [type, handler, useCapture, target = document] of listeners) {
      target.removeEventListener(type, handler, useCapture);
    }

    listeners.length = 0;
  }

  function startCapture(startOptions = {}) {
    if (state.capturing) {
      return getBundle();
    }

    state.bundle = createInitialBundle(
      {
        ...options,
        ...startOptions,
        document,
      },
      now,
    );
    state.capturing = true;
    state.annotationMode = false;
    state.lastScrollX = windowObject?.scrollX ?? 0;
    state.lastScrollY = windowObject?.scrollY ?? 0;

    overlay?.clear?.();
    overlay?.setAnnotationMode?.(false);
    wireListeners();

    const startedNavigate = {
      type: 'navigate',
      url: state.bundle.metadata.baseUrl ?? windowObject?.location?.href ?? document?.location?.href ?? '',
    };
    recordStep(startedNavigate);
    appendDebugEvent({ type: 'capture-start' });

    return getBundle();
  }

  function stopCapture() {
    if (!state.capturing) {
      return state.bundle ? getBundle() : null;
    }

    state.capturing = false;
    state.annotationMode = false;
    overlay?.setAnnotationMode?.(false);
    unwireListeners();
    appendDebugEvent({ type: 'capture-stop' });
    return getBundle();
  }

  function isCapturing() {
    return state.capturing;
  }

  return {
    startCapture,
    stopCapture,
    getBundle,
    isCapturing,
    recordStep,
    recordAnnotation,
  };
}
