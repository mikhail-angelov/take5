// Tests for extension/lib/event-capture.js: the click press->release span (docs/plans/
// 20260822-action-model-and-robust-pass-a.md Task 1).

import { test } from "node:test";
import assert from "node:assert";
import "../lib/event-capture.js";

const { createEventCapture } = globalThis.__demoRecorderEventCapture;

// Minimal fake DOM EventTarget: addEventListener/removeEventListener plus a synchronous
// dispatch a test can call directly, mirroring FakeEventTarget's style in
// recording-lifecycle-module.test.js for chrome.* fakes.
class FakeEventTarget {
  constructor() {
    this.listeners = new Map();
  }

  addEventListener(type, handler) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(handler);
  }

  removeEventListener(type, handler) {
    const list = this.listeners.get(type);
    if (!list) return;
    this.listeners.set(
      type,
      list.filter((h) => h !== handler),
    );
  }

  dispatch(type, event) {
    for (const handler of this.listeners.get(type) || []) handler(event);
  }
}

function setup() {
  const document = new FakeEventTarget();
  const emitted = [];
  let t = 0;
  const capture = createEventCapture({
    now: () => t,
    emit: (event) => emitted.push(event),
    describeTarget: () => null,
  });
  const originalDocument = globalThis.document;
  globalThis.document = document;
  capture.attach();
  return {
    document,
    emitted,
    setTime: (ms) => {
      t = ms;
    },
    restore: () => {
      capture.detach();
      globalThis.document = originalDocument;
    },
  };
}

function pointerDown(x, y, extra = {}) {
  return { button: 0, pointerType: "mouse", clientX: x, clientY: y, ...extra };
}

function pointerUp(x, y, extra = {}) {
  return { clientX: x, clientY: y, ...extra };
}

function click(x, y, extra = {}) {
  return { isTrusted: true, clientX: x, clientY: y, button: 0, target: null, ...extra };
}

test("an ordinary click carries startMs/endMs matching pointerdown/pointerup", () => {
  const { document, emitted, setTime, restore } = setup();
  try {
    setTime(100);
    document.dispatch("pointerdown", pointerDown(10, 10));
    setTime(180);
    document.dispatch("pointerup", pointerUp(10, 10));
    setTime(180);
    document.dispatch("click", click(10, 10));

    const clicks = emitted.filter((e) => e.kind === "click");
    assert.strictEqual(clicks.length, 1);
    assert.strictEqual(clicks[0].startMs, 100);
    assert.strictEqual(clicks[0].endMs, 180);
  } finally {
    restore();
  }
});

test("a click with no preceding pointerdown stays instant (defensive path)", () => {
  const { document, emitted, setTime, restore } = setup();
  try {
    setTime(50);
    document.dispatch("click", click(5, 5));

    const clicks = emitted.filter((e) => e.kind === "click");
    assert.strictEqual(clicks.length, 1);
    assert.strictEqual(clicks[0].startMs, undefined);
    assert.strictEqual(clicks[0].endMs, undefined);
  } finally {
    restore();
  }
});

test("a click following a gesture that became a drag does not inherit the drag's span", () => {
  const { document, emitted, setTime, restore } = setup();
  try {
    setTime(0);
    document.dispatch("pointerdown", pointerDown(0, 0));
    setTime(200);
    // Moves far enough and long enough to qualify as a drag.
    document.dispatch("pointerup", pointerUp(50, 50));
    setTime(200);
    document.dispatch("click", click(50, 50));

    const drags = emitted.filter((e) => e.kind === "drag");
    assert.strictEqual(drags.length, 1);
    assert.strictEqual(drags[0].startMs, 0);
    assert.strictEqual(drags[0].endMs, 200);

    const clicks = emitted.filter((e) => e.kind === "click");
    assert.strictEqual(clicks.length, 1);
    assert.strictEqual(clicks[0].startMs, undefined);
    assert.strictEqual(clicks[0].endMs, undefined);
  } finally {
    restore();
  }
});
