import test from 'node:test';
import assert from 'node:assert/strict';
import {
  speedConfig,
  shouldRenderCursorAction,
  buildInputZoomReport,
  buildRenderPipeline,
  renderVideo,
} from './render.js';

test('speedConfig keeps user actions real-time and trims idle harder with cutDelays', () => {
  assert.equal(speedConfig().duringUserAction, 1.0);
  assert.equal(speedConfig({ cutDelays: false }).duringIdle, 3.0);
  assert.equal(speedConfig({ cutDelays: true }).duringIdle, 6.0);
});

test('speedConfig fast-forwards transit gaps with no nearby user action', () => {
  const rule = speedConfig().rules.find((item) => item.name === 'compress-transit-gaps');
  // Mid-build: far from any user action on both sides → compressed.
  assert.equal(rule.match({ timeSinceLastAction: 8000, timeUntilNextAction: 9000 }), true);
  // Right after / before an action → kept real-time so the interaction reads.
  assert.equal(rule.match({ timeSinceLastAction: 100, timeUntilNextAction: 9000 }), false);
  assert.equal(rule.match({ timeSinceLastAction: 9000, timeUntilNextAction: 100 }), false);
  // cutDelays compresses the dead time harder.
  const cutDelaysRule = speedConfig({ cutDelays: true }).rules.find(
    (item) => item.name === 'compress-transit-gaps',
  );
  assert.ok(cutDelaysRule.speed > rule.speed);
});

test('speedConfig keeps three-second caption dwells at real time', () => {
  const rules = speedConfig().rules;
  const captionDwellRule = rules.find((rule) => rule.name === 'keep-caption-dwells');
  assert.equal(
    captionDwellRule.match({
      activeActions: [{ method: 'waitForTimeout', startTime: 1000, endTime: 4000 }],
    }),
    true,
  );
  assert.equal(
    captionDwellRule.match({
      activeActions: [{ method: 'waitForTimeout', startTime: 1000, endTime: 3999 }],
    }),
    false,
  );
  assert.equal(captionDwellRule.speed, 1.0);
});

test('shouldRenderCursorAction keeps click and drag boundaries, not every drag sample', () => {
  assert.equal(shouldRenderCursorAction({ method: 'click' }), true);
  assert.equal(shouldRenderCursorAction({ method: 'mouseDown' }), true);
  assert.equal(shouldRenderCursorAction({ method: 'mouseUp' }), true);
  assert.equal(shouldRenderCursorAction({ method: 'mouseMove' }), false);
  assert.equal(shouldRenderCursorAction({ method: 'fill' }), false);
});

test('buildInputZoomReport anchors typing to the preceding click on the same field', () => {
  const report = buildInputZoomReport(
    [
      { method: 'click', params: { selector: '#prompt' }, point: { x: 80, y: 90 } },
      { method: 'type', params: { selector: '#prompt' } },
      { method: 'click', params: { selector: '#send' }, point: { x: 95, y: 90 } },
    ],
    { width: 100, height: 100 },
    ['Click prompt', 'Enter text', 'Click send'],
  );
  assert.deepEqual(report, [
    { zoom: null },
    { zoom: { x: 0.8, y: 0.9, level: 1.8 } },
    { zoom: null },
  ]);
});

// A fake Recast builder that records the chained stage calls.
function fakeRecast(calls) {
  const proxy = new Proxy(
    {},
    {
      get(_target, prop) {
        return (...args) => {
          calls.push({ method: prop, args });
          return proxy;
        };
      },
    },
  );
  return { from: (...args) => (calls.push({ method: 'from', args }), proxy) };
}

test('buildRenderPipeline derives trace subtitles for zoom windows and does not burn them', () => {
  const calls = [];
  buildRenderPipeline(fakeRecast(calls), '/run');
  const methods = calls.map((c) => c.method);
  assert.deepEqual(methods, [
    'from',
    'parse',
    'speedUp',
    'subtitlesFromTrace',
    'autoZoom',
    'cursorOverlay',
    'clickEffect',
    'render',
  ]);
  assert.equal(calls.find((c) => c.method === 'render').args[0].burnSubtitles, false);
  const cursorOverlay = calls.find((c) => c.method === 'cursorOverlay').args[0];
  const { filter } = cursorOverlay;
  assert.equal(filter, shouldRenderCursorAction);
  assert.equal(filter({ method: 'mouseMove' }), false);
  assert.equal(cursorOverlay.moveDurationMs, 400);
  assert.equal(cursorOverlay.hideAfterMs, 1000);
  // autoZoom must come after a subtitle stage (recast binds zoom to caption windows).
  assert.ok(methods.indexOf('subtitlesFromTrace') < methods.indexOf('autoZoom'));
  assert.equal(calls.find((c) => c.method === 'autoZoom').args[0].clickLevel, 1.0);
});

test('buildRenderPipeline applies explicit input zooms before auto zoom', () => {
  const calls = [];
  buildRenderPipeline(fakeRecast(calls), '/run', {
    captions: ['Enter text'],
    inputZoomReport: [{ zoom: { x: 0.8, y: 0.9, level: 1.8 } }],
  });
  const methods = calls.map((call) => call.method);
  assert.ok(methods.indexOf('enrichZoomFromReport') < methods.indexOf('autoZoom'));
});

test('buildRenderPipeline burns narration captions and skips trace subtitles', () => {
  const calls = [];
  buildRenderPipeline(fakeRecast(calls), '/run', { captions: ['Open app', 'Click go'] });
  const methods = calls.map((c) => c.method);
  assert.ok(!methods.includes('subtitlesFromTrace'));
  const sub = calls.find((c) => c.method === 'subtitles');
  assert.equal(typeof sub.args[0], 'function'); // recast textFn
  assert.equal(calls.find((c) => c.method === 'render').args[0].burnSubtitles, true);
  assert.ok(methods.indexOf('subtitles') < methods.indexOf('autoZoom'));
});

test('buildRenderPipeline uses source SRT captions without auto zoom', () => {
  const calls = [];
  buildRenderPipeline(fakeRecast(calls), '/run', {
    captions: ['Open app'],
    captionsSrtPath: '/run/captions.source.srt',
  });
  const methods = calls.map((call) => call.method);
  assert.ok(methods.includes('subtitlesFromSrt'));
  assert.ok(!methods.includes('subtitles'));
  assert.ok(!methods.includes('autoZoom'));
});

test('renderVideo executes the pipeline terminal and returns the out path', async () => {
  let written = null;
  const Recast = {
    from() {
      const p = {
        parse: () => p,
        speedUp: () => p,
        subtitlesFromTrace: () => p,
        autoZoom: () => p,
        cursorOverlay: () => p,
        clickEffect: () => p,
        render: () => p,
        toFile: async (out) => {
          written = out;
        },
      };
      return p;
    },
  };
  const result = await renderVideo({ sourceDir: '/run', outPath: '/run/demo.mp4', Recast });
  assert.equal(result, '/run/demo.mp4');
  assert.equal(written, '/run/demo.mp4');
});

test('renderVideo requires sourceDir and outPath', async () => {
  await assert.rejects(() => renderVideo({ outPath: '/x' }), /sourceDir/);
  await assert.rejects(() => renderVideo({ sourceDir: '/x' }), /outPath/);
});
