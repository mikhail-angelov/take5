import test from 'node:test';
import assert from 'node:assert/strict';
import {
  describeStep,
  buildStepCaptions,
  buildCaptionSrtEntries,
  createCaptionTextFn,
  naturalizeCaptions,
} from './captions.js';

test('describeStep renders each meaningful step type and skips the rest', () => {
  assert.equal(
    describeStep({ type: 'navigate', url: 'https://app.test/dash/' }),
    'Open app.test/dash',
  );
  assert.equal(describeStep({ type: 'click', selector: '#greet' }), 'Click greet');
  assert.equal(describeStep({ type: 'fill', selector: '#name', value: 'Ada' }), 'Enter “Ada”');
  assert.equal(describeStep({ type: 'fill', selector: '#name' }), 'Fill in name');
  assert.equal(describeStep({ type: 'select', value: 'EU' }), 'Select “EU”');
  // Keystrokes are intentionally left uncaptioned but keep their trace slot.
  assert.equal(describeStep({ type: 'keypress', key: 'Enter' }), '');
  assert.equal(describeStep({ type: 'assert_text', text: 'Hi' }), null);
  assert.equal(describeStep({ type: 'scroll' }), null);
});

test('describeStep prefers a human label over the raw selector for clicks', () => {
  assert.equal(
    describeStep({ type: 'click', selector: '#app > div:nth-of-type(3) > button', label: 'Run' }),
    'Click Run',
  );
  assert.equal(
    describeStep({ type: 'fill', selector: 'textarea', label: 'Describe a change' }),
    'Fill in Describe a change',
  );
});

test('buildStepCaptions keeps only meaningful steps, in order', () => {
  const plan = {
    steps: [
      { type: 'navigate', url: 'https://x.test' },
      { type: 'wait', ms: 100 },
      { type: 'fill', selector: '#n', value: 'Ada' },
      { type: 'assert_text', text: 'ok' },
      { type: 'click', selector: '#go' },
    ],
  };
  assert.deepEqual(buildStepCaptions(plan), ['Open x.test', 'Enter “Ada”', 'Click go']);
});

test('buildStepCaptions keeps an empty slot for keypresses to stay aligned with the trace', () => {
  const plan = {
    steps: [
      { type: 'click', selector: '#code', label: 'Code editor' },
      { type: 'keypress', key: '7' },
      { type: 'keypress', key: '0' },
      { type: 'click', selector: '#run', label: 'Run' },
    ],
  };
  assert.deepEqual(buildStepCaptions(plan), ['Click Code editor', '', '', 'Click Run']);
});

test('createCaptionTextFn puts a caption on the dwell following its visible action', () => {
  const textFn = createCaptionTextFn(['Open x', 'Click go']);
  assert.equal(textFn({ method: 'goto' }), '');
  assert.equal(textFn({ method: 'waitForTimeout' }), 'Open x');
  assert.equal(textFn({ method: 'press', params: { key: 'ControlOrMeta+A' } }), '');
  assert.equal(textFn({ method: 'fill' }), '');
  assert.equal(textFn({ method: 'evaluateExpression' }), ''); // callout inject — skipped, no consume
  assert.equal(textFn({ method: 'waitForTimeout' }), ''); // callout hold — skipped
  assert.equal(textFn({ method: 'type' }), '');
  assert.equal(textFn({ method: 'waitForTimeout' }), 'Click go');
  assert.equal(textFn({ method: 'click' }), ''); // captions exhausted
});

test('createCaptionTextFn still consumes the empty slot for a planned keypress', () => {
  const textFn = createCaptionTextFn(['Click editor', '', 'Click run']);
  assert.equal(textFn({ method: 'click' }), '');
  assert.equal(textFn({ method: 'waitForTimeout' }), 'Click editor');
  assert.equal(textFn({ method: 'press', params: { key: '7' } }), '');
  assert.equal(textFn({ method: 'waitForTimeout' }), '');
  assert.equal(textFn({ method: 'click' }), '');
  assert.equal(textFn({ method: 'waitForTimeout' }), 'Click run');
});

test('buildCaptionSrtEntries uses the three-second dwell before its action', () => {
  const entries = buildCaptionSrtEntries(
    [
      { method: 'waitForTimeout', startTime: 220, endTime: 900 },
      { method: 'waitForTimeout', startTime: 900, endTime: 3900 },
      { method: 'goto', startTime: 4000, endTime: 4100 },
    ],
    ['Open app'],
    100,
  );
  assert.deepEqual(entries, [{ index: 1, startMs: 800, endMs: 3800, text: 'Open app' }]);
});

test('naturalizeCaptions returns input unchanged when no API key is set', async () => {
  const out = await naturalizeCaptions(['Open x', 'Click go'], { env: {} });
  assert.deepEqual(out, ['Open x', 'Click go']);
});

test('naturalizeCaptions rewrites captions via the LLM when configured', async () => {
  const fetchImpl = async () => ({
    ok: true,
    json: async () => ({
      choices: [{ message: { content: '["Let us open the app", "Now click Go"]' } }],
    }),
  });
  const out = await naturalizeCaptions(['Open x', 'Click go'], {
    env: { TAKE5_LLM_API_KEY: 'sk-test' },
    fetchImpl,
  });
  assert.deepEqual(out, ['Let us open the app', 'Now click Go']);
});

test('naturalizeCaptions falls back to input on a bad LLM response', async () => {
  const fetchImpl = async () => ({ ok: false, status: 500 });
  const out = await naturalizeCaptions(['Open x'], {
    env: { TAKE5_LLM_API_KEY: 'sk-test' },
    fetchImpl,
  });
  assert.deepEqual(out, ['Open x']);
});

test('naturalizeCaptions falls back when the LLM returns a wrong-length array', async () => {
  const fetchImpl = async () => ({
    ok: true,
    json: async () => ({ choices: [{ message: { content: '["only one"]' } }] }),
  });
  const out = await naturalizeCaptions(['Open x', 'Click go'], {
    env: { TAKE5_LLM_API_KEY: 'sk-test' },
    fetchImpl,
  });
  assert.deepEqual(out, ['Open x', 'Click go']);
});
