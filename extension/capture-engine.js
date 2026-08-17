import { validateCaptureBundle } from './capture-schema.js';

function log(...args) {
  try {
    console.log('[Take5:capture]', ...args);
  } catch {
    // console may be unavailable in some contexts.
  }
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

function cssEscape(value) {
  const text = String(value);
  if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') {
    return CSS.escape(text);
  }

  // Minimal fallback for environments without CSS.escape (e.g. tests).
  return text.replace(/[^a-zA-Z0-9_-]/g, (char) => `\\${char}`);
}

function attributeSelector(element, attribute) {
  const value = element.getAttribute?.(attribute);
  if (!isNonEmptyString(value)) {
    return null;
  }

  const tag = String(element.tagName || '*').toLowerCase();
  return `${tag}[${attribute}="${value.replace(/"/g, '\\"')}"]`;
}

function nthOfTypeIndex(element) {
  let index = 1;
  let sibling = element.previousElementSibling;
  while (sibling) {
    if (sibling.tagName === element.tagName) {
      index += 1;
    }
    sibling = sibling.previousElementSibling;
  }
  return index;
}

// Builds a unique CSS path (anchored at the nearest id) so every interactable
// element yields a locator even when it has no id/name/aria-label of its own.
function buildDomPath(element) {
  const segments = [];
  let node = element;

  while (node && node.nodeType === 1 && node.tagName) {
    const tag = String(node.tagName).toLowerCase();

    if (isNonEmptyString(node.id)) {
      segments.unshift(`#${cssEscape(node.id.trim())}`);
      return segments.join(' > ');
    }

    if (tag === 'html' || tag === 'body') {
      segments.unshift(tag);
      break;
    }

    segments.unshift(`${tag}:nth-of-type(${nthOfTypeIndex(node)})`);
    node = node.parentElement;
  }

  return segments.length > 0 ? segments.join(' > ') : null;
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
    return `#${cssEscape(element.id.trim())}`;
  }

  const stableAttribute =
    attributeSelector(element, 'data-testid') ??
    attributeSelector(element, 'name') ??
    attributeSelector(element, 'aria-label');
  if (stableAttribute) {
    return stableAttribute;
  }

  // Last resort: a structural path so the step is always locatable.
  return buildDomPath(element);
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
  return [
    'Enter',
    'Tab',
    'Escape',
    'Backspace',
    'ArrowUp',
    'ArrowDown',
    'ArrowLeft',
    'ArrowRight',
  ].includes(key);
}

function isModifierKey(key) {
  return ['Control', 'Shift', 'Alt', 'Meta', 'CapsLock'].includes(key);
}

// A printable character produces a single-character `event.key` (e.g. "7", "a").
function isPrintableKey(key) {
  return typeof key === 'string' && key.length === 1;
}

function isCheckboxLike(target) {
  const type = String(target?.type || '').toLowerCase();
  return type === 'checkbox' || type === 'radio';
}

function isLabelElement(target) {
  return String(target?.tagName || '').toLowerCase() === 'label';
}

// Rich code/text editors (Monaco, CodeMirror) route keystrokes through a hidden
// proxy <textarea> and never emit a `change` carrying the document text. Such a
// field is not a plain form control, so its edits must be captured key-by-key.
function isEditorProxyField(target) {
  if (!isFormField(target)) {
    return false;
  }
  if (String(target.getAttribute?.('aria-hidden')) === 'true') {
    return true;
  }
  return (
    typeof target.closest === 'function' &&
    Boolean(
      target.closest('.monaco-editor, .cm-editor, .CodeMirror, [data-mode-id], [role="code"]'),
    )
  );
}

// A plain form control emits a `change` we record as a single fill/select step,
// so its individual keystrokes must NOT also be recorded (double application).
function capturesValueViaChange(target) {
  if (!isFormField(target) && !isSelectField(target)) {
    return false;
  }
  return !isEditorProxyField(target);
}

function collapseWhitespace(value) {
  return String(value ?? '')
    .replace(/\s+/g, ' ')
    .trim();
}

const LABEL_MAX_LENGTH = 80;
const DRAG_THRESHOLD_PX = 8;

function readViewport(windowObject) {
  const width = Number(windowObject?.innerWidth);
  const height = Number(windowObject?.innerHeight);
  return {
    width: Number.isFinite(width) && width > 0 ? width : 1280,
    height: Number.isFinite(height) && height > 0 ? height : 720,
  };
}

// Human-readable name for a control, used to caption the replay. Prefers an
// explicit aria-label, then a form control's associated <label>/placeholder or a
// select's chosen option, and finally the element's own visible text.
function computeStepLabel(element) {
  if (!element || element.nodeType !== 1) {
    return undefined;
  }

  const aria = element.getAttribute?.('aria-label');
  if (isNonEmptyString(aria)) {
    return collapseWhitespace(aria).slice(0, LABEL_MAX_LENGTH);
  }

  const tag = String(element.tagName || '').toLowerCase();

  if (tag === 'select') {
    const option = element.options?.[element.selectedIndex];
    const optionText = collapseWhitespace(option?.textContent);
    if (optionText) {
      return optionText.slice(0, LABEL_MAX_LENGTH);
    }
  }

  if (tag === 'input' || tag === 'textarea') {
    const associated = element.labels?.[0];
    const associatedText = collapseWhitespace(associated?.textContent);
    if (associatedText) {
      return associatedText.slice(0, LABEL_MAX_LENGTH);
    }
    const placeholder = element.getAttribute?.('placeholder');
    if (isNonEmptyString(placeholder)) {
      return collapseWhitespace(placeholder).slice(0, LABEL_MAX_LENGTH);
    }
    const title = element.getAttribute?.('title');
    if (isNonEmptyString(title)) {
      return collapseWhitespace(title).slice(0, LABEL_MAX_LENGTH);
    }
    return undefined;
  }

  const text = collapseWhitespace(element.textContent);
  if (text) {
    return text.slice(0, LABEL_MAX_LENGTH);
  }

  const title = element.getAttribute?.('title');
  if (isNonEmptyString(title)) {
    return collapseWhitespace(title).slice(0, LABEL_MAX_LENGTH);
  }

  return undefined;
}

function isOverlayTarget(target) {
  return (
    typeof target?.closest === 'function' &&
    Boolean(
      target.closest('[data-take5-overlay-root], [data-take5-prompt], [data-take5-prompt-panel]'),
    )
  );
}

function createInitialBundle(options, now) {
  const startedAt = getTimestamp(now);
  const scenarioId =
    (isNonEmptyString(options.scenarioId) ? options.scenarioId.trim() : null) ??
    `capture-${startedAt.replace(/[:.]/g, '-')}-${Math.random().toString(16).slice(2, 8)}`;

  return {
    metadata: {
      schemaVersion: 2,
      scenarioId,
      name: options.name ?? options.title ?? scenarioId,
      baseUrl: options.baseUrl ?? null,
      viewportPreset: options.viewportPreset ?? 'desktop-1280',
      extensionVersion: options.extensionVersion ?? '0.1.0',
      startedAt,
      updatedAt: startedAt,
      viewport: readViewport(options.window),
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
  const monotonicNow =
    options.monotonicNow ?? (() => globalThis.performance?.now?.() ?? Date.now());
  const promptForAnnotation =
    options.promptForAnnotation ?? overlay?.promptForAnnotation?.bind(overlay);
  const onUpdate = typeof options.onUpdate === 'function' ? options.onUpdate : null;
  const listeners = [];

  function notifyUpdate() {
    if (!onUpdate || !state.bundle) {
      return;
    }

    try {
      onUpdate(getBundle());
    } catch (error) {
      // A failing observer must not break capture, but surface why.
      log('notifyUpdate skipped:', error.message);
    }
  }

  const state = {
    bundle: null,
    capturing: false,
    annotationMode: false,
    lastScrollX: 0,
    lastScrollY: 0,
    pointerGesture: null,
    suppressClickTarget: null,
  };

  function recordStep(step) {
    if (!state.bundle) {
      return null;
    }

    const nextStep = {
      ...step,
      id: step.id ?? toStepId(state.bundle.steps.length),
    };
    for (const key of Object.keys(nextStep)) {
      if (nextStep[key] === undefined) {
        delete nextStep[key];
      }
    }
    state.bundle.steps.push(nextStep);
    state.bundle.metadata.updatedAt = getTimestamp(now);
    log(
      'recordStep',
      nextStep.type,
      nextStep.selector ?? '',
      '-> total',
      state.bundle.steps.length,
    );
    notifyUpdate();
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
      pageUrl:
        windowObject?.location?.href ??
        document?.location?.href ??
        state.bundle.metadata.baseUrl ??
        '',
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
    notifyUpdate();
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

    if (isOverlayTarget(event.target)) {
      return;
    }

    appendDebugEvent({ type: 'keydown', key: event.key });

    if (event.key === 'Control') {
      state.annotationMode = true;
      overlay?.setAnnotationMode?.(true);
      return;
    }

    if (isModifierKey(event.key)) {
      return;
    }

    const target = event.target ?? null;

    // A plain form field's final value is captured once via its `change` event,
    // so we only record its navigation keys (e.g. Enter to submit) here. A code
    // editor or other non-form surface emits no such change, so we reconstruct
    // its edits key-by-key, including printable characters.
    if (capturesValueViaChange(target)) {
      if (isNavigationKey(event.key)) {
        recordStep({ type: 'keypress', key: event.key });
      }
      return;
    }

    if (isPrintableKey(event.key) || isNavigationKey(event.key)) {
      recordStep({ type: 'keypress', key: event.key });
    }
  }

  function handleKeyup(event) {
    if (!state.capturing) {
      return;
    }

    if (isOverlayTarget(event.target)) {
      return;
    }

    appendDebugEvent({ type: 'keyup', key: event.key });

    if (event.key === 'Control') {
      state.annotationMode = false;
      overlay?.setAnnotationMode?.(false);
    }
  }

  function handleClick(event) {
    log('DOM click fired; capturing =', state.capturing, '; target =', event.target?.tagName);
    if (!state.capturing) {
      return;
    }

    const target = event.target ?? event.currentTarget ?? null;
    if (isOverlayTarget(target)) {
      log('click ignored: overlay target');
      return;
    }
    appendDebugEvent({ type: 'click', selector: resolveElementSelector(target) });

    if (state.suppressClickTarget && state.suppressClickTarget === target) {
      state.suppressClickTarget = null;
      return;
    }

    if (state.annotationMode || event.ctrlKey) {
      event.preventDefault?.();
      event.stopPropagation?.();
      event.stopImmediatePropagation?.();
      maybePromptAnnotation(target).catch(() => {});
      return;
    }

    // Activating a <label> also dispatches a click on its associated control,
    // which we record instead. Recording the label click too would toggle the
    // control twice on replay (net no-op for a checkbox).
    if (isLabelElement(target)) {
      log('click ignored: label (its control click is recorded instead)');
      return;
    }

    recordStep({
      type: 'click',
      selector: resolveElementSelector(target) ?? undefined,
      ref: target?.dataset?.take5Ref ?? undefined,
      label: computeStepLabel(target),
    });
  }

  function handleInput(event) {
    if (!state.capturing) {
      return;
    }

    const target = event.target ?? null;
    if (isOverlayTarget(target)) {
      return;
    }
    const selector = resolveElementSelector(target);
    if (selector && isFormField(target)) {
      appendDebugEvent({ type: 'input', selector });
    }
  }

  function handleChange(event) {
    log('DOM change fired; capturing =', state.capturing, '; target =', event.target?.tagName);
    if (!state.capturing) {
      return;
    }

    const target = event.target ?? null;
    if (isOverlayTarget(target)) {
      return;
    }
    const selector = resolveElementSelector(target);
    if (!selector) {
      return;
    }

    if (isSelectField(target)) {
      recordStep({
        type: 'select',
        selector,
        value: readTargetValue(target),
        label: computeStepLabel(target),
      });
      return;
    }

    // A checkbox/radio toggle is already recorded as its click step; a `fill`
    // with the input's "on" value is both redundant and invalid to replay.
    if (isCheckboxLike(target)) {
      return;
    }

    // A code editor's proxy textarea can emit a change with proxy-only text; its
    // edits are captured key-by-key instead, so ignore the change here.
    if (isEditorProxyField(target)) {
      return;
    }

    if (isFormField(target)) {
      recordStep({
        type: 'fill',
        selector,
        value: readTargetValue(target),
        label: computeStepLabel(target),
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

  function eventTime(event) {
    const timestamp = Number(event?.timeStamp);
    return Number.isFinite(timestamp) ? timestamp : monotonicNow();
  }

  function addPointerPoint(gesture, event) {
    const t = Math.max(gesture.points.at(-1)?.t ?? 0, eventTime(event) - gesture.startedAt);
    gesture.points.push({ x: event.clientX, y: event.clientY, t });
  }

  function handlePointerdown(event) {
    if (
      !state.capturing ||
      isOverlayTarget(event.target) ||
      (event.button !== undefined && event.button !== 0)
    ) {
      return;
    }

    state.pointerGesture = {
      pointerId: event.pointerId ?? 1,
      target: event.target ?? null,
      selector: resolveElementSelector(event.target ?? null),
      startedAt: eventTime(event),
      points: [],
    };
    addPointerPoint(state.pointerGesture, event);
  }

  function handlePointermove(event) {
    const gesture = state.pointerGesture;
    if (!state.capturing || !gesture || (event.pointerId ?? 1) !== gesture.pointerId) {
      return;
    }

    const coalesced = event.getCoalescedEvents?.() ?? [];
    for (const point of coalesced) {
      addPointerPoint(gesture, point);
    }
    addPointerPoint(gesture, event);
  }

  function handlePointerup(event) {
    const gesture = state.pointerGesture;
    if (!state.capturing || !gesture || (event.pointerId ?? 1) !== gesture.pointerId) {
      return;
    }

    addPointerPoint(gesture, event);
    state.pointerGesture = null;

    const first = gesture.points[0];
    const last = gesture.points.at(-1);
    if (!first || !last || Math.hypot(last.x - first.x, last.y - first.y) < DRAG_THRESHOLD_PX) {
      return;
    }

    recordStep({
      type: 'pointer_drag',
      selector: gesture.selector ?? undefined,
      points: gesture.points,
    });
    state.suppressClickTarget = gesture.target;
    globalThis.setTimeout?.(() => {
      state.suppressClickTarget = null;
    }, 0);
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
      ['pointerdown', handlePointerdown, true],
      ['pointermove', handlePointermove, true],
      ['pointerup', handlePointerup, true],
    ];

    for (const [type, handler, useCapture] of listenerPairs) {
      document.addEventListener(type, handler, useCapture);
      listeners.push([type, handler, useCapture]);
    }
    log('wired', listenerPairs.length, 'listeners on document', document?.location?.href ?? '');

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
    log('startCapture called; already capturing =', state.capturing);
    if (state.capturing) {
      return getBundle();
    }

    state.bundle = createInitialBundle(
      {
        ...options,
        ...startOptions,
        document,
        window: windowObject,
      },
      now,
    );
    state.capturing = true;
    state.annotationMode = false;
    state.lastScrollX = windowObject?.scrollX ?? 0;
    state.lastScrollY = windowObject?.scrollY ?? 0;
    state.pointerGesture = null;
    state.suppressClickTarget = null;

    overlay?.clear?.();
    overlay?.setAnnotationMode?.(false);
    wireListeners();

    const startedNavigate = {
      type: 'navigate',
      url:
        state.bundle.metadata.baseUrl ??
        windowObject?.location?.href ??
        document?.location?.href ??
        '',
    };
    recordStep(startedNavigate);
    appendDebugEvent({ type: 'capture-start' });

    return getBundle();
  }

  function resumeCapture(existingBundle) {
    log('resumeCapture called; already capturing =', state.capturing);
    if (state.capturing) {
      return getBundle();
    }

    // Trust a bundle previously produced by getBundle (already validated) so an
    // in-progress capture continues across a full page navigation.
    state.bundle = cloneValue(validateCaptureBundle(cloneValue(existingBundle)));
    state.capturing = true;
    state.annotationMode = false;
    state.lastScrollX = windowObject?.scrollX ?? 0;
    state.lastScrollY = windowObject?.scrollY ?? 0;
    state.pointerGesture = null;
    state.suppressClickTarget = null;

    overlay?.setAnnotationMode?.(false);
    wireListeners();
    appendDebugEvent({ type: 'capture-resume' });

    const url =
      windowObject?.location?.href ??
      document?.location?.href ??
      state.bundle.metadata.baseUrl ??
      '';
    if (url && state.bundle.steps.at(-1)?.url !== url) {
      recordStep({ type: 'navigate', url });
    } else {
      notifyUpdate();
    }

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
    resumeCapture,
    stopCapture,
    getBundle,
    isCapturing,
    recordStep,
    recordAnnotation,
  };
}
