import test from 'node:test';
import assert from 'node:assert/strict';
import { createCaptureEngine } from './capture-engine.js';

function createFakeElement(selector) {
  return {
    selector,
    id: selector.startsWith('#') ? selector.slice(1) : '',
    tagName: 'BUTTON',
    value: '',
    textContent: 'Launch',
    getBoundingClientRect() {
      return { x: 10, y: 20, width: 80, height: 30 };
    },
  };
}

function createFakeDocument() {
  const listeners = new Map();
  const elements = new Map();
  const body = {
    classList: { add() {}, remove() {} },
    style: {},
  };

  return {
    body,
    documentElement: body,
    listeners,
    elements,
    title: 'Demo app',
    location: { href: 'https://example.com/app' },
    addEventListener(type, handler) {
      listeners.set(type, handler);
    },
    removeEventListener(type, handler) {
      if (listeners.get(type) === handler) {
        listeners.delete(type);
      }
    },
    querySelector(selector) {
      return elements.get(selector) ?? null;
    },
    register(selector, element) {
      elements.set(selector, element);
    },
  };
}

function createFakeOverlay() {
  const calls = [];
  return {
    calls,
    setAnnotationMode(value) {
      calls.push(['setAnnotationMode', value]);
    },
    async promptForAnnotation() {
      calls.push(['promptForAnnotation']);
      return 'Highlight this';
    },
    pinAnnotation(annotation) {
      calls.push(['pinAnnotation', annotation]);
    },
    clear() {
      calls.push(['clear']);
    },
  };
}

function trigger(document, type, event) {
  const listener = document.listeners.get(type);
  assert.ok(listener, `missing listener for ${type}`);
  listener({
    preventDefault() {},
    stopPropagation() {},
    stopImmediatePropagation() {},
    currentTarget: event?.currentTarget ?? event?.target ?? null,
    target: event?.target ?? null,
    ctrlKey: Boolean(event?.ctrlKey),
    key: event?.key,
    value: event?.value,
    button: event?.button,
    pointerId: event?.pointerId,
    clientX: event?.clientX,
    clientY: event?.clientY,
    timeStamp: event?.timeStamp,
    getCoalescedEvents: event?.getCoalescedEvents,
  });
}

test('createCaptureEngine records semantic steps and converts Ctrl-clicks into annotations', async () => {
  const document = createFakeDocument();
  const overlay = createFakeOverlay();
  const button = createFakeElement('#launch');
  document.register('#launch', button);

  const engine = createCaptureEngine({
    document,
    overlay,
    now: () => '2026-05-26T10:00:00.000Z',
  });

  engine.startCapture({
    scenarioId: 'capture-demo',
    name: 'Capture demo',
    baseUrl: 'https://example.com/app',
  });

  trigger(document, 'click', { target: button });

  trigger(document, 'keydown', { key: 'Control', ctrlKey: true, target: button });
  trigger(document, 'click', { target: button, ctrlKey: true });
  trigger(document, 'keyup', { key: 'Control', ctrlKey: false, target: button });

  await Promise.resolve();

  const bundle = engine.getBundle();

  assert.equal(bundle.metadata.scenarioId, 'capture-demo');
  assert.deepEqual(
    bundle.steps.map((step) => step.type),
    ['navigate', 'click'],
  );
  assert.equal(bundle.annotations.length, 1);
  assert.deepEqual(bundle.annotations[0], {
    description: 'Highlight this',
    selector: '#launch',
    pageUrl: 'https://example.com/app',
    createdAt: '2026-05-26T10:00:00.000Z',
    orderIndex: 0,
    stepIndex: 1,
    nearestStepId: 'step-2',
    targetRect: { x: 10, y: 20, width: 80, height: 30 },
  });
  assert.ok(overlay.calls.some(([type, value]) => type === 'setAnnotationMode' && value === true));
  assert.ok(overlay.calls.some(([type]) => type === 'promptForAnnotation'));
  assert.equal(overlay.calls.at(-1)[0], 'pinAnnotation');
});

test('capture derives a structural selector for elements without id/name/aria-label', () => {
  const document = createFakeDocument();
  const body = { nodeType: 1, tagName: 'BODY', parentElement: null, previousElementSibling: null };
  const div = { nodeType: 1, tagName: 'DIV', parentElement: body, previousElementSibling: null };
  const firstButton = {
    nodeType: 1,
    tagName: 'BUTTON',
    parentElement: div,
    previousElementSibling: null,
  };
  const button = {
    nodeType: 1,
    tagName: 'BUTTON',
    parentElement: div,
    previousElementSibling: firstButton,
    getAttribute: () => null,
    value: '',
  };

  const engine = createCaptureEngine({
    document,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'no-ids', baseUrl: 'https://example.com/app' });

  trigger(document, 'click', { target: button });

  const bundle = engine.getBundle();
  const click = bundle.steps.find((step) => step.type === 'click');
  assert.ok(click, 'a click step was recorded');
  assert.equal(click.selector, 'body > div:nth-of-type(1) > button:nth-of-type(2)');
});

test('resumeCapture continues an existing bundle across a navigation and records the new page', () => {
  const firstDoc = createFakeDocument();
  const firstButton = createFakeElement('#launch');
  firstDoc.register('#launch', firstButton);

  const firstEngine = createCaptureEngine({
    document: firstDoc,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  firstEngine.startCapture({ scenarioId: 'capture-demo', baseUrl: 'https://example.com/app' });
  trigger(firstDoc, 'click', { target: firstButton });
  const carried = firstEngine.stopCapture();
  assert.deepEqual(
    carried.steps.map((s) => s.type),
    ['navigate', 'click'],
  );

  // Simulate a full navigation: a brand new content script / engine on a new page.
  const secondDoc = createFakeDocument();
  secondDoc.location.href = 'https://example.com/next';
  const secondButton = createFakeElement('#confirm');
  secondDoc.register('#confirm', secondButton);

  const updates = [];
  const secondEngine = createCaptureEngine({
    document: secondDoc,
    window: { location: { href: 'https://example.com/next' } },
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:01:00.000Z',
    onUpdate: (bundle) => updates.push(bundle.steps.length),
  });

  secondEngine.resumeCapture(carried);
  assert.equal(secondEngine.isCapturing(), true);

  trigger(secondDoc, 'click', { target: secondButton });

  const bundle = secondEngine.getBundle();
  assert.deepEqual(
    bundle.steps.map((s) => s.type),
    ['navigate', 'click', 'navigate', 'click'],
  );
  assert.equal(bundle.steps.at(-2).url, 'https://example.com/next');
  assert.equal(bundle.metadata.scenarioId, 'capture-demo');
  assert.ok(updates.length >= 2, 'onUpdate fires for resume navigate and the post-nav click');
});

test('capture records printable keystrokes into a contenteditable editor as keypress steps', () => {
  const document = createFakeDocument();
  // A code-editor line: contenteditable, not a form field, so no `change` fires.
  const editorLine = {
    nodeType: 1,
    tagName: 'DIV',
    getAttribute: () => null,
    textContent: 'WIDTH = 50',
  };

  const engine = createCaptureEngine({
    document,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'edit-code', baseUrl: 'https://example.com/app' });

  trigger(document, 'keydown', { key: '7', target: editorLine });
  trigger(document, 'keydown', { key: '0', target: editorLine });
  trigger(document, 'keydown', { key: 'Backspace', target: editorLine });
  // Modifiers never produce a step.
  trigger(document, 'keydown', { key: 'Shift', target: editorLine });

  const bundle = engine.getBundle();
  assert.deepEqual(
    bundle.steps.map((step) => (step.type === 'keypress' ? step.key : step.type)),
    ['navigate', '7', '0', 'Backspace'],
  );
});

test('capture records keystrokes into a Monaco-style proxy textarea (aria-hidden editor input)', () => {
  const document = createFakeDocument();
  // Monaco routes typing through a hidden, aria-hidden <textarea> inside the
  // editor; it never emits a useful change, so keystrokes must be recorded.
  const proxy = {
    nodeType: 1,
    tagName: 'TEXTAREA',
    value: '',
    getAttribute: (name) => (name === 'aria-hidden' ? 'true' : null),
    closest: (sel) => (sel.includes('monaco-editor') ? {} : null),
  };

  const engine = createCaptureEngine({
    document,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'monaco', baseUrl: 'https://example.com/app' });

  trigger(document, 'keydown', { key: '7', target: proxy });
  trigger(document, 'keydown', { key: '0', target: proxy });
  // A change firing on the proxy must not become a fill step.
  trigger(document, 'change', { target: proxy });

  const bundle = engine.getBundle();
  assert.deepEqual(
    bundle.steps.map((step) => (step.type === 'keypress' ? step.key : step.type)),
    ['navigate', '7', '0'],
  );
  assert.ok(!bundle.steps.some((step) => step.type === 'fill'));
});

test('capture treats a plain textarea as a single fill, not per-key steps', () => {
  const document = createFakeDocument();
  const textarea = {
    nodeType: 1,
    tagName: 'TEXTAREA',
    selector: '#chat',
    value: 'hi',
    getAttribute: () => null,
    closest: () => null,
    labels: [],
  };

  const engine = createCaptureEngine({
    document,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'chat', baseUrl: 'https://example.com/app' });

  trigger(document, 'keydown', { key: 'h', target: textarea });
  trigger(document, 'keydown', { key: 'i', target: textarea });
  trigger(document, 'change', { target: textarea });

  const bundle = engine.getBundle();
  assert.deepEqual(
    bundle.steps.map((step) => step.type),
    ['navigate', 'fill'],
    'printable keys are absorbed by the single fill from change',
  );
  assert.equal(bundle.steps.at(-1).value, 'hi');
});

test('capture skips redundant checkbox change and records the toggle via its control click', () => {
  const document = createFakeDocument();
  const checkbox = {
    nodeType: 1,
    tagName: 'INPUT',
    type: 'checkbox',
    selector: 'input[data-testid="viewer-wireframe"]',
    value: 'on',
    getAttribute: () => null,
    labels: [{ textContent: 'wireframe' }],
  };
  const label = {
    nodeType: 1,
    tagName: 'LABEL',
    getAttribute: () => null,
    textContent: 'wireframe',
  };

  const engine = createCaptureEngine({
    document,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'wireframe', baseUrl: 'https://example.com/app' });

  // A user clicks the <label>; the browser also dispatches a click on the input,
  // then a change event. Only one toggle step should survive, with a label.
  trigger(document, 'click', { target: label });
  trigger(document, 'click', { target: checkbox });
  trigger(document, 'change', { target: checkbox });

  const bundle = engine.getBundle();
  const clicks = bundle.steps.filter((step) => step.type === 'click');
  assert.equal(clicks.length, 1, 'exactly one toggle click is recorded');
  assert.equal(clicks[0].selector, 'input[data-testid="viewer-wireframe"]');
  assert.equal(clicks[0].label, 'wireframe');
  assert.ok(
    !bundle.steps.some((step) => step.type === 'fill'),
    'no invalid fill("on") step for the checkbox',
  );
});

test('capture attaches a human label to click steps from the element text', () => {
  const document = createFakeDocument();
  const runButton = {
    nodeType: 1,
    tagName: 'BUTTON',
    selector: '#run',
    id: 'run',
    value: '',
    getAttribute: () => null,
    textContent: 'Run ▷',
  };
  document.register('#run', runButton);

  const engine = createCaptureEngine({
    document,
    overlay: createFakeOverlay(),
    now: () => '2026-05-26T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'labels', baseUrl: 'https://example.com/app' });

  trigger(document, 'click', { target: runButton });

  const bundle = engine.getBundle();
  const click = bundle.steps.find((step) => step.type === 'click');
  assert.equal(click.label, 'Run ▷');
});

test('capture records every timed pointer point as a drag and suppresses its trailing click', () => {
  const document = createFakeDocument();
  const canvas = createFakeElement('#viewer');
  document.register('#viewer', canvas);
  const engine = createCaptureEngine({
    document,
    window: { innerWidth: 1440, innerHeight: 900, location: document.location },
    overlay: createFakeOverlay(),
    now: () => '2026-08-03T10:00:00.000Z',
  });
  engine.startCapture({ scenarioId: 'rotate-model', baseUrl: 'https://example.com/app' });

  trigger(document, 'pointerdown', {
    target: canvas,
    button: 0,
    pointerId: 7,
    clientX: 100,
    clientY: 100,
    timeStamp: 100,
  });
  trigger(document, 'pointermove', {
    target: canvas,
    pointerId: 7,
    clientX: 110,
    clientY: 104,
    timeStamp: 112,
    getCoalescedEvents: () => [{ clientX: 104, clientY: 101, timeStamp: 106 }],
  });
  trigger(document, 'pointermove', {
    target: canvas,
    pointerId: 7,
    clientX: 120,
    clientY: 110,
    timeStamp: 124,
  });
  trigger(document, 'pointerup', {
    target: canvas,
    pointerId: 7,
    clientX: 120,
    clientY: 110,
    timeStamp: 140,
  });
  trigger(document, 'click', { target: canvas });

  const bundle = engine.getBundle();
  assert.equal(bundle.metadata.schemaVersion, 2);
  assert.deepEqual(bundle.metadata.viewport, { width: 1440, height: 900 });
  assert.deepEqual(
    bundle.steps.map((step) => step.type),
    ['navigate', 'pointer_drag'],
  );
  assert.deepEqual(bundle.steps[1], {
    id: 'step-2',
    type: 'pointer_drag',
    selector: '#viewer',
    points: [
      { x: 100, y: 100, t: 0 },
      { x: 104, y: 101, t: 6 },
      { x: 110, y: 104, t: 12 },
      { x: 120, y: 110, t: 24 },
      { x: 120, y: 110, t: 40 },
    ],
  });
});

test('createCaptureEngine generates a scenario id when startCapture receives an empty one', () => {
  const document = createFakeDocument();
  const overlay = createFakeOverlay();

  const engine = createCaptureEngine({
    document,
    overlay,
    now: () => '2026-05-26T10:00:00.000Z',
  });

  engine.startCapture({
    scenarioId: '',
    name: 'Capture demo',
    baseUrl: 'https://example.com/app',
  });

  const bundle = engine.getBundle();

  assert.match(bundle.metadata.scenarioId, /^capture-2026-05-26T10-00-00-000Z-/);
});
