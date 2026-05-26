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
