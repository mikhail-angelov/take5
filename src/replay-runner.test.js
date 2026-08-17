import test from 'node:test';
import assert from 'node:assert/strict';
import {
  resolveViewport,
  resolveLocator,
  planStepToAction,
  buildCalloutMarkup,
  resolvePatOrigin,
  minimumCaptionDwellMs,
  replayPointerDrag,
  typeIntoLocator,
} from './replay-runner.js';

test('resolveViewport maps known presets and falls back to desktop-1280', () => {
  assert.deepEqual(resolveViewport('mobile-390'), { width: 390, height: 844 });
  assert.deepEqual(resolveViewport('desktop-1280'), { width: 1280, height: 720 });
  assert.deepEqual(resolveViewport('unknown'), { width: 1280, height: 720 });
  assert.deepEqual(resolveViewport(undefined), { width: 1280, height: 720 });
});

test('resolveViewport prefers an exact capture viewport over the preset', () => {
  assert.deepEqual(resolveViewport({ width: 1512, height: 982 }, 'desktop-1280'), {
    width: 1512,
    height: 982,
  });
});

test('resolveLocator prefers a trimmed selector and ignores refs', () => {
  assert.equal(resolveLocator({ selector: '  #email ' }), '#email');
  assert.equal(resolveLocator({ ref: 'e12' }), null);
  assert.equal(resolveLocator({ selector: '   ' }), null);
});

test('planStepToAction maps every supported step type', () => {
  assert.deepEqual(planStepToAction({ type: 'navigate', url: 'https://x' }), {
    kind: 'navigate',
    url: 'https://x',
  });
  assert.deepEqual(planStepToAction({ type: 'click', selector: '#go' }), {
    kind: 'click',
    locator: '#go',
  });
  assert.deepEqual(planStepToAction({ type: 'fill', selector: '#e', value: 'a' }), {
    kind: 'fill',
    locator: '#e',
    value: 'a',
  });
  assert.deepEqual(planStepToAction({ type: 'select', selector: '#s', value: 'b' }), {
    kind: 'select',
    locator: '#s',
    value: 'b',
  });
  assert.deepEqual(planStepToAction({ type: 'keypress', key: 'Enter' }), {
    kind: 'keypress',
    key: 'Enter',
  });
  assert.deepEqual(planStepToAction({ type: 'wait', ms: 500 }), { kind: 'wait', ms: 500 });
  assert.deepEqual(planStepToAction({ type: 'assert_text', selector: '#t', text: 'Hi' }), {
    kind: 'assert_text',
    locator: '#t',
    text: 'Hi',
  });
  assert.deepEqual(
    planStepToAction({
      type: 'pointer_drag',
      points: [
        { x: 1, y: 2, t: 0 },
        { x: 3, y: 4, t: 12 },
      ],
    }),
    {
      kind: 'pointer_drag',
      points: [
        { x: 1, y: 2, t: 0 },
        { x: 3, y: 4, t: 12 },
      ],
    },
  );
  assert.deepEqual(
    planStepToAction({ type: 'fill', selector: '#prompt', value: 'Hello', typingDelayMs: 12 }),
    { kind: 'fill', locator: '#prompt', value: 'Hello', typingDelayMs: 12 },
  );
});

test('typeIntoLocator clears with keyboard input and types with the selected delay', async () => {
  const calls = [];
  const target = {
    press: async (key) => calls.push(['press', key]),
    pressSequentially: async (value, options) => calls.push(['pressSequentially', value, options]),
  };
  await typeIntoLocator(
    { locator: (selector) => (calls.push(['locator', selector]), target) },
    '#prompt',
    'Hi',
    12,
  );
  assert.deepEqual(calls, [
    ['locator', '#prompt'],
    ['press', 'ControlOrMeta+A'],
    ['press', 'Backspace'],
    ['pressSequentially', 'Hi', { delay: 12 }],
  ]);
});

test('replayPointerDrag preserves each recorded point and timing gap', async () => {
  const calls = [];
  const page = {
    mouse: {
      move: async (x, y) => calls.push(['move', x, y]),
      down: async () => calls.push(['down']),
      up: async () => calls.push(['up']),
    },
    waitForTimeout: async (ms) => calls.push(['wait', ms]),
  };
  await replayPointerDrag(page, [
    { x: 10, y: 20, t: 0 },
    { x: 14, y: 23, t: 16 },
    { x: 20, y: 30, t: 40 },
  ]);
  assert.deepEqual(calls, [
    ['move', 10, 20],
    ['down'],
    ['wait', 16],
    ['move', 14, 23],
    ['wait', 24],
    ['move', 20, 30],
    ['up'],
  ]);
});

test('planStepToAction converts scroll direction into a signed delta', () => {
  assert.equal(planStepToAction({ type: 'scroll', direction: 'down', amount: 200 }).delta, 200);
  assert.equal(planStepToAction({ type: 'scroll', direction: 'up', amount: 200 }).delta, -200);
});

test('minimumCaptionDwellMs holds captioned actions for at least three seconds', () => {
  assert.equal(minimumCaptionDwellMs({ kind: 'click' }, 900), 3100);
  assert.equal(minimumCaptionDwellMs({ kind: 'navigate' }, 4500), 4500);
  assert.equal(minimumCaptionDwellMs({ kind: 'keypress' }, 0), 0);
  assert.equal(minimumCaptionDwellMs({ kind: 'scroll' }, 900), 900);
});

test('buildCalloutMarkup keeps raw text (textContent handles safety) and carries position', () => {
  const rect = { x: 10, y: 20, width: 30, height: 40 };
  const markup = buildCalloutMarkup({
    description: '  <b>Click</b> "here"  ',
    selector: '#go',
    targetRect: rect,
  });
  assert.equal(markup.text, '<b>Click</b> "here"');
  assert.equal(markup.selector, '#go');
  assert.deepEqual(markup.targetRect, rect);

  const empty = buildCalloutMarkup({ description: '   ' });
  assert.equal(empty.text, '');
  assert.equal(empty.selector, null);
  assert.equal(empty.targetRect, null);
});

test('resolvePatOrigin derives the app origin from the first navigate step', () => {
  const plan = {
    steps: [
      { type: 'wait', ms: 10 },
      { type: 'navigate', url: 'https://text2part.bconf.com/app?x=1' },
      { type: 'navigate', url: 'https://other.example.com' },
    ],
  };
  assert.equal(resolvePatOrigin(plan), 'https://text2part.bconf.com');
});

test('resolvePatOrigin returns null when no navigable url exists', () => {
  assert.equal(resolvePatOrigin({ steps: [{ type: 'click', selector: '#go' }] }), null);
  assert.equal(resolvePatOrigin({ steps: [{ type: 'navigate', url: 'not a url' }] }), null);
  assert.equal(resolvePatOrigin({}), null);
});

test('buildCalloutMarkup drops a malformed targetRect', () => {
  const markup = buildCalloutMarkup({
    description: 'x',
    targetRect: { x: 1, y: 2, width: 'nope', height: 4 },
  });
  assert.equal(markup.targetRect, null);
});
