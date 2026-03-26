import test from 'node:test';
import assert from 'node:assert/strict';
import { Browser, __testables } from './browser.js';

function createFakeLocator(log, label) {
  return {
    label,
    first() {
      return this;
    },
    async scrollIntoViewIfNeeded() {
      log.push(['scrollIntoViewIfNeeded', label]);
    },
    async fill(value) {
      log.push(['fill', label, value]);
    },
    async hover() {
      log.push(['hover', label]);
    },
    async click() {
      log.push(['click', label]);
    },
    async selectOption(option) {
      log.push(['selectOption', label, option]);
    },
    async boundingBox() {
      return { x: 10, y: 20, width: 30, height: 40 };
    },
  };
}

test('browser_fill uses ref locator for normal textboxes', async () => {
  __testables.parseSnapshotForRefMap('- textbox "Email" [ref=e1]');

  const calls = [];
  const browser = new Browser();
  browser.page = {
    locator(selector) {
      return {
        first() {
          return {
            async click() {
              calls.push(['contenteditable-click', selector]);
            },
          };
        },
      };
    },
    getByRole(role, options = {}) {
      calls.push(['getByRole', role, options]);
      return createFakeLocator(calls, `${role}:${options.name ?? ''}`);
    },
    async waitForSelector() {
      throw new Error('waitForSelector should not be used for ref fill');
    },
    async fill() {
      throw new Error('page.fill should not be used for ref fill');
    },
    keyboard: {
      async type() {
        throw new Error('keyboard.type fallback should not be used');
      },
      async press() {
        throw new Error('keyboard.press fallback should not be used');
      },
      async down() {
        throw new Error('keyboard.down fallback should not be used');
      },
      async up() {
        throw new Error('keyboard.up fallback should not be used');
      },
    },
  };

  const result = await browser.executeTool('browser_fill', {
    ref: 'e1',
    value: 'hello@example.com',
  });

  assert.equal(result, 'Filled with "hello@example.com"');
  assert.deepEqual(calls, [
    ['getByRole', 'textbox', { name: 'Email', exact: true }],
    ['scrollIntoViewIfNeeded', 'textbox:Email'],
    ['fill', 'textbox:Email', 'hello@example.com'],
  ]);
});

test('browser_wait honors requested duration', async () => {
  const browser = new Browser();
  const calls = [];
  browser.page = {
    async waitForTimeout(ms) {
      calls.push(ms);
    },
  };

  const result = await browser.executeTool('browser_wait', { ms: 250 });

  assert.equal(result, 'Waited 250ms');
  assert.deepEqual(calls, [250]);
});

test('browser_click highlights target before clicking', async () => {
  __testables.parseSnapshotForRefMap('- button "Run" [ref=e9]');

  const calls = [];
  const browser = new Browser();
  browser.page = {
    async evaluate(fn, payload) {
      calls.push(['evaluate', payload]);
    },
    getByRole(role, options = {}) {
      calls.push(['getByRole', role, options]);
      return createFakeLocator(calls, `${role}:${options.name ?? ''}`);
    },
    locator(selector) {
      return {
        first() {
          return this;
        },
        async click() {
          calls.push(['locator.click', selector]);
          throw new Error('no data-ref attribute');
        },
      };
    },
    async waitForTimeout(ms) {
      calls.push(['waitForTimeout', ms]);
    },
  };

  const result = await browser.executeTool('browser_click', { ref: 'e9' });

  assert.equal(result, 'Clicked successfully');
  assert.deepEqual(calls, [
    ['getByRole', 'button', { name: 'Run', exact: true }],
    ['scrollIntoViewIfNeeded', 'button:Run'],
    [
      'evaluate',
      {
        description: 'Clicking here',
        selector: null,
        targetRect: { x: 10, y: 20, width: 30, height: 40 },
      },
    ],
    ['waitForTimeout', 250],
    ['locator.click', '[data-ref="e9"]'],
    ['getByRole', 'button', { name: 'Run', exact: true }],
    ['scrollIntoViewIfNeeded', 'button:Run'],
    ['click', 'button:Run'],
    ['evaluate', undefined],
    ['waitForTimeout', 20],
  ]);
});

test('inject_annotation resolves ref-like selectors to bounding boxes', async () => {
  __testables.parseSnapshotForRefMap('- textbox "Editor" [ref=e210]');

  const calls = [];
  const browser = new Browser();
  browser.page = {
    async evaluate(fn, payload) {
      calls.push(payload);
    },
    getByRole(role, options = {}) {
      calls.push(['getByRole', role, options]);
      return createFakeLocator(calls, `${role}:${options.name ?? ''}`);
    },
  };

  const result = await browser.executeTool('inject_annotation', {
    description: 'Typing solution',
    selector: 'textbox[ref="e210"]',
  });

  assert.equal(result, 'Annotation shown: "Typing solution"');
  assert.deepEqual(calls, [
    ['getByRole', 'textbox', { name: 'Editor', exact: true }],
    ['scrollIntoViewIfNeeded', 'textbox:Editor'],
    {
      description: 'Typing solution',
      selector: null,
      targetRect: { x: 10, y: 20, width: 30, height: 40 },
    },
  ]);
});

test('browser_click scrolls selector targets into view before clicking', async () => {
  const calls = [];
  const browser = new Browser();
  browser.page = {
    async evaluate(fn, payload) {
      calls.push(['evaluate', payload]);
    },
    async waitForSelector(selector, options) {
      calls.push(['waitForSelector', selector, options]);
    },
    locator(selector) {
      return {
        label: selector,
        first() {
          return this;
        },
        async scrollIntoViewIfNeeded() {
          calls.push(['scrollIntoViewIfNeeded', selector]);
        },
        async click(options) {
          calls.push(['click', selector, options]);
        },
      };
    },
    async waitForTimeout(ms) {
      calls.push(['waitForTimeout', ms]);
    },
  };

  const result = await browser.executeTool('browser_click', {
    selector: '#submit-button',
  });

  assert.equal(result, 'Clicked successfully');
  assert.deepEqual(calls, [
    [
      'evaluate',
      {
        description: 'Clicking here',
        selector: '#submit-button',
        targetRect: null,
      },
    ],
    ['waitForTimeout', 250],
    ['waitForSelector', '#submit-button', { timeout: 6000 }],
    ['scrollIntoViewIfNeeded', '#submit-button'],
    ['click', '#submit-button', { timeout: 6000 }],
    ['evaluate', undefined],
    ['waitForTimeout', 20],
  ]);
});
