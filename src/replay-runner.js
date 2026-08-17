/* global document, window, location */
import path from 'node:path';
import fs from 'node:fs/promises';
import { chromium as defaultChromium } from 'playwright';
import { MIN_CAPTION_DURATION_MS } from './captions.js';

// Named viewport presets carried in the capture metadata. The extension records
// a preset label; the runner maps it to concrete dimensions here.
export const VIEWPORT_PRESETS = {
  'desktop-1280': { width: 1280, height: 720 },
  'desktop-1440': { width: 1440, height: 900 },
  'laptop-1024': { width: 1024, height: 768 },
  'mobile-390': { width: 390, height: 844 },
};

const DEFAULT_VIEWPORT = VIEWPORT_PRESETS['desktop-1280'];
const CALLOUT_HOLD_MS = 1400;
const ASSERT_TIMEOUT_MS = 120000;
const DEFAULT_TYPING_DELAY_MS = 45;
// Captures rarely include pauses, so raw actions fire back-to-back and the render
// has no frames to glide the cursor, settle a zoom, or let a viewer read the
// result. Hold briefly after each interaction; captioned actions are extended
// below to their minimum readable duration.
const ACTION_DWELL_MS = 900;
const CAPTION_LEAD_IN_MS = 100;
const CAPTION_DWELL_ACTION_KINDS = new Set(['navigate', 'click', 'fill', 'select']);
// Kinds that move the UI and deserve a dwell. Wait/assert are their own sync
// points; noop carries nothing to look at.
const DWELL_ACTION_KINDS = new Set([
  'navigate',
  'click',
  'fill',
  'select',
  'keypress',
  'scroll',
  'pointer_drag',
]);

/** Keep displayed captions on screen long enough to read. */
export function minimumCaptionDwellMs(action, actionDwellMs) {
  if (!CAPTION_DWELL_ACTION_KINDS.has(action.kind)) {
    return actionDwellMs;
  }
  return Math.max(actionDwellMs, MIN_CAPTION_DURATION_MS + CAPTION_LEAD_IN_MS);
}

export function resolveViewport(viewportOrPreset, preset) {
  if (typeof viewportOrPreset === 'string' && preset === undefined) {
    return VIEWPORT_PRESETS[viewportOrPreset] ?? DEFAULT_VIEWPORT;
  }
  const viewport = viewportOrPreset;
  if (
    viewport &&
    Number.isFinite(viewport.width) &&
    viewport.width > 0 &&
    Number.isFinite(viewport.height) &&
    viewport.height > 0
  ) {
    return { width: viewport.width, height: viewport.height };
  }
  return VIEWPORT_PRESETS[preset] ?? DEFAULT_VIEWPORT;
}

// The PAT bootstrap (SPEC22) seeds the token into the app origin's sessionStorage
// before navigation; the origin is taken from the plan's first navigate step.
export function resolvePatOrigin(plan) {
  const nav = (plan?.steps ?? []).find(
    (step) => step.type === 'navigate' && typeof step.url === 'string',
  );
  if (!nav) {
    return null;
  }
  try {
    return new URL(nav.url).origin;
  } catch {
    return null;
  }
}

// Prefer a CSS selector; the accessibility `ref` from the capture snapshot is
// only valid within that snapshot and cannot be replayed in a fresh session.
export function resolveLocator(step) {
  if (typeof step.selector === 'string' && step.selector.trim().length > 0) {
    return step.selector.trim();
  }
  return null;
}

// Pure mapping from a plan step to an intent the runner executes. Kept separate
// from browser I/O so it can be unit-tested without Playwright.
export function planStepToAction(step) {
  switch (step.type) {
    case 'navigate':
      return { kind: 'navigate', url: step.url };
    case 'click':
      return { kind: 'click', locator: resolveLocator(step) };
    case 'fill':
      return {
        kind: 'fill',
        locator: resolveLocator(step),
        value: step.value,
        ...(typeof step.typingDelayMs === 'number' ? { typingDelayMs: step.typingDelayMs } : {}),
      };
    case 'select':
      return { kind: 'select', locator: resolveLocator(step), value: step.value };
    case 'keypress':
      return { kind: 'keypress', key: step.key };
    case 'scroll':
      return { kind: 'scroll', delta: step.direction === 'up' ? -step.amount : step.amount };
    case 'wait':
      return { kind: 'wait', ms: step.ms };
    case 'assert_text':
      return { kind: 'assert_text', locator: resolveLocator(step), text: step.text };
    case 'pointer_drag':
      return { kind: 'pointer_drag', points: step.points };
    default:
      return { kind: 'noop' };
  }
}

function normalizeRect(rect) {
  if (!rect || typeof rect !== 'object') {
    return null;
  }
  const { x, y, width, height } = rect;
  if ([x, y, width, height].some((v) => typeof v !== 'number' || !Number.isFinite(v))) {
    return null;
  }
  return { x, y, width, height };
}

// Positioning payload for an in-page positional callout (feature 7). The text is
// raw (assigned via textContent downstream, which is XSS-safe on its own); the
// selector and captured targetRect give a deterministic position.
export function buildCalloutMarkup(annotation) {
  return {
    text: String(annotation?.description ?? '').trim(),
    selector: resolveLocator(annotation),
    targetRect: normalizeRect(annotation?.targetRect),
  };
}

function collectStepAnnotations(step) {
  return Array.isArray(step.annotations) ? step.annotations : [];
}

// Injected into the page to draw a callout near a target element (or bottom-center
// as a fallback) and keep it on screen for `holdMs`.
async function showCallout(page, annotation, holdMs) {
  const markup = buildCalloutMarkup(annotation);
  if (!markup.text) {
    return;
  }
  await page.evaluate((markup) => {
    const node = document.createElement('div');
    node.setAttribute('data-take5-callout', '');
    node.textContent = markup.text;
    Object.assign(node.style, {
      position: 'fixed',
      zIndex: '2147483647',
      maxWidth: '320px',
      padding: '10px 14px',
      borderRadius: '10px',
      background: 'rgba(17, 24, 39, 0.95)',
      color: '#fff',
      font: '500 14px/1.4 -apple-system, Segoe UI, Roboto, sans-serif',
      boxShadow: '0 8px 24px rgba(0,0,0,0.35)',
      pointerEvents: 'none',
    });
    // Prefer the element's live position; fall back to the captured rect; then
    // to bottom-centre only when neither is available. The selector may be
    // Playwright-only syntax (e.g. :has-text) that DOM querySelector rejects, so
    // guard it rather than let the callout crash the run.
    let target = null;
    if (markup.selector) {
      try {
        target = document.querySelector(markup.selector);
      } catch {
        target = null;
      }
    }
    const liveRect = target?.getBoundingClientRect?.();
    const rect = liveRect && liveRect.width > 0 ? liveRect : markup.targetRect;
    if (rect) {
      node.style.left = `${Math.max(8, Math.min(rect.x, window.innerWidth - 340))}px`;
      node.style.top = `${Math.max(8, rect.y + rect.height + 8)}px`;
    } else {
      node.style.left = '50%';
      node.style.bottom = '32px';
      node.style.transform = 'translateX(-50%)';
    }
    document.body.appendChild(node);
  }, markup);
  await page.waitForTimeout(holdMs);
  await page.evaluate(() => {
    document.querySelectorAll('[data-take5-callout]').forEach((node) => node.remove());
  });
}

async function runAction(page, action) {
  switch (action.kind) {
    case 'navigate':
      await page.goto(action.url, { waitUntil: 'load' });
      return;
    case 'click':
      requireLocator(action);
      await page.click(action.locator);
      return;
    case 'fill':
      requireLocator(action);
      await typeIntoLocator(page, action.locator, action.value, action.typingDelayMs);
      return;
    case 'select':
      requireLocator(action);
      await page.selectOption(action.locator, String(action.value ?? ''));
      return;
    case 'keypress':
      await page.keyboard.press(action.key);
      return;
    case 'scroll':
      await page.mouse.wheel(0, action.delta);
      return;
    case 'wait':
      await page.waitForTimeout(action.ms);
      return;
    case 'assert_text': {
      // Generous timeout: assert_text doubles as a sync point for slow async work
      // (e.g. an LLM build), which can outlast Playwright's default 30s.
      const target = action.locator
        ? page.locator(action.locator).filter({ hasText: action.text }).first()
        : page.getByText(action.text, { exact: false }).first();
      await target.waitFor({ timeout: ASSERT_TIMEOUT_MS });
      return;
    }
    case 'pointer_drag':
      await replayPointerDrag(page, action.points);
      return;
    default:
      return;
  }
}

export async function typeIntoLocator(
  page,
  locator,
  value,
  typingDelayMs = DEFAULT_TYPING_DELAY_MS,
) {
  const target = page.locator(locator);
  // Clear with keyboard input instead of `fill('')`: the latter creates a
  // separate trace action before `type`, which can steal the zoom window from
  // the visible typing action during rendering.
  await target.press('ControlOrMeta+A');
  await target.press('Backspace');
  await target.pressSequentially(String(value ?? ''), { delay: typingDelayMs });
}

export async function replayPointerDrag(page, points) {
  if (!Array.isArray(points) || points.length < 2) {
    throw new Error('pointer_drag requires at least two points');
  }

  await page.mouse.move(points[0].x, points[0].y);
  await page.mouse.down();
  let previousTime = points[0].t;
  for (const point of points.slice(1)) {
    const delay = Math.max(0, point.t - previousTime);
    if (delay > 0) {
      await page.waitForTimeout(delay);
    }
    await page.mouse.move(point.x, point.y);
    previousTime = point.t;
  }
  await page.mouse.up();
}

function requireLocator(action) {
  if (!action.locator) {
    throw new Error(`step "${action.kind}" has no replayable selector`);
  }
}

// After the app has loaded via a PAT bootstrap, fail loudly if the exchange was
// rejected — otherwise the whole demo would silently run on the free trial.
async function assertPatAuth(page) {
  // A failed exchange sets data-auth-error on the app root ([data-state], per
  // SPEC22 §3), not on <html>; wait for the root, then branch on it.
  await page.waitForSelector('[data-state]', { timeout: 15000 }).catch(() => {});
  const authError = await page.evaluate(() => {
    const root = document.querySelector('[data-state]');
    return root ? root.getAttribute('data-auth-error') : null;
  });
  if (authError) {
    throw new Error(`PAT bootstrap failed: data-auth-error="${authError}"`);
  }
}

const SETTLE_STATES = ['idle', 'done', 'awaiting-input', 'error'];

// Read the SPEC22 state-machine revision, or null on apps that don't expose one.
async function readStateRev(page) {
  return page.evaluate(() => {
    const el = document.querySelector('[data-state]');
    if (!el) return null;
    const rev = Number(el.getAttribute('data-state-rev'));
    return Number.isFinite(rev) ? rev : null;
  });
}

// Wait for a SPEC22 state machine to settle after an action. `prevRev` is the
// revision read *before* the action; because rev advances synchronously at action
// start, a mutating action (send / run / revert) already shows rev > prevRev here
// and we wait for a terminal state, while a non-mutating one (rotate, toggle)
// leaves rev unchanged and resolves immediately. When prevRev is null the app
// either has no state machine (el absent → resolves at once) or just loaded, in
// which case we wait for the initial/restored turn to settle before proceeding.
async function settleState(page, prevRev, timeoutMs) {
  await page
    .waitForFunction(
      ({ prev, terminal }) => {
        const el = document.querySelector('[data-state]');
        if (!el) return true; // non-SPEC22 app
        const settled =
          terminal.includes(el.getAttribute('data-state')) &&
          el.getAttribute('aria-busy') !== 'true';
        if (prev == null) return settled; // initial load / restored session
        const rev = Number(el.getAttribute('data-state-rev'));
        if (!(rev > prev)) return true; // nothing mutated
        return settled;
      },
      { prev: prevRev, terminal: SETTLE_STATES },
      { timeout: timeoutMs },
    )
    .catch(() => {});
}

/**
 * Replay a normalized plan in a headless browser, injecting positional callouts,
 * and record a Playwright trace + video into `outputDir`. Returns the source
 * directory to hand to the renderer.
 */
export async function replayPlan(plan, options = {}) {
  const {
    outputDir,
    chromium = defaultChromium,
    logger = { info() {}, debug() {} },
    headless = true,
    calloutHoldMs = CALLOUT_HOLD_MS,
    userDataDir = null,
    channel = null,
    authUrl = null,
    pat = null,
    actionDwellMs = ACTION_DWELL_MS,
  } = options;

  if (!outputDir) {
    throw new Error('replayPlan requires an outputDir');
  }

  if (pat && userDataDir) {
    throw new Error('PAT bootstrap requires a fresh context; cannot combine with userDataDir');
  }

  // Resolve the app origin up front so a misconfigured plan fails before launch.
  const patOrigin = pat ? resolvePatOrigin(plan) : null;
  if (pat && !patOrigin) {
    throw new Error('PAT bootstrap needs a navigate step to derive the app origin');
  }

  const viewport = resolveViewport(plan.viewport, plan.viewportPreset);
  const warnings = [];
  const consoleErrors = [];

  // With a userDataDir we launch a persistent context bound to a real browser
  // profile, so the replay inherits its logged-in (incl. httpOnly) session.
  // channel:'chrome' is required on macOS so cookie decryption can reach the
  // real Chrome's Keychain entry.
  let browser = null;
  let context;
  if (userDataDir) {
    context = await chromium.launchPersistentContext(userDataDir, {
      channel: channel ?? 'chrome',
      headless,
      viewport,
      recordVideo: { dir: outputDir, size: viewport },
    });
  } else {
    browser = await chromium.launch({ headless });
    context = await browser.newContext({
      viewport,
      recordVideo: { dir: outputDir, size: viewport },
    });
  }
  // Seed the PAT into the app origin's sessionStorage as a document-start init
  // script (SPEC22 shape A). Guards keep it to the top frame and exact origin so
  // it never leaks into third-party iframes; the single-use marker (set before
  // the token, regardless of exchange outcome) stops a reload re-seeding it.
  if (pat) {
    await context.addInitScript(
      ({ token, appOrigin }) => {
        if (window.top !== window) return;
        if (location.origin !== appOrigin) return;
        if (sessionStorage.getItem('easycad_pat_used')) return;
        sessionStorage.setItem('easycad_pat_used', '1');
        sessionStorage.setItem('easycad_pat', token);
      },
      { token: pat, appOrigin: patOrigin },
    );
  }

  await context.tracing.start({ screenshots: true, snapshots: true, sources: true });
  const page = context.pages()[0] ?? (await context.newPage());

  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      consoleErrors.push(msg.text());
    }
  });
  page.on('pageerror', (error) => consoleErrors.push(String(error?.message ?? error)));

  let videoPath;
  try {
    // Establish an authenticated session before the plan runs (e.g. a magic-link
    // callback that sets the auth cookie). Not part of the plan, so it is never
    // captioned or persisted; the URL stays a runtime-only argument.
    if (authUrl) {
      logger.debug('replay: visiting auth url');
      await page.goto(authUrl, { waitUntil: 'load' });
      await page.waitForTimeout(1200);
    }

    let patChecked = false;
    for (const step of plan.steps) {
      const action = planStepToAction(step);
      logger.debug(`replay ${step.id}: ${action.kind}`);
      const dwellMs = minimumCaptionDwellMs(action, actionDwellMs);
      // Show the instruction while the UI is still in the state the action will
      // operate on, rather than describing it after it has already happened.
      if (dwellMs > 0 && CAPTION_DWELL_ACTION_KINDS.has(action.kind)) {
        await page.waitForTimeout(dwellMs);
      }
      // Snapshot the state-machine revision before the action so we can tell,
      // afterwards, whether it started a turn worth waiting out.
      const prevRev = await readStateRev(page);
      try {
        await runAction(page, action);
      } catch (error) {
        warnings.push(`step ${step.id} (${action.kind}) failed: ${error.message}`);
        await page
          .screenshot({ path: path.join(outputDir, `failure-${step.id}.png`) })
          .catch(() => {});
        throw error;
      }

      // The plan's first navigation is the app load that consumes the seeded PAT;
      // verify the exchange before the demo proceeds on the wrong identity.
      if (pat && !patChecked && action.kind === 'navigate') {
        patChecked = true;
        await assertPatAuth(page);
      }

      // Never fire the next step while an async turn (LLM build, code run, revert)
      // is still in flight — captures rarely include explicit waits.
      await settleState(page, prevRev, ASSERT_TIMEOUT_MS);

      // Non-captioned interactions still get a brief post-action settle beat.
      if (
        actionDwellMs > 0 &&
        DWELL_ACTION_KINDS.has(action.kind) &&
        !CAPTION_DWELL_ACTION_KINDS.has(action.kind)
      ) {
        await page.waitForTimeout(actionDwellMs);
      }

      for (const annotation of collectStepAnnotations(step)) {
        await showCallout(page, annotation, calloutHoldMs);
      }
    }

    // Annotations the plan could not link to a step still carry context; surface
    // them at the end so valid captures never silently drop callouts.
    for (const annotation of Array.isArray(plan.annotations) ? plan.annotations : []) {
      await showCallout(page, annotation, calloutHoldMs);
    }
  } finally {
    await context.tracing.stop({ path: path.join(outputDir, 'trace.zip') }).catch(() => {});
    const video = page.video();
    // Video is finalized on context close.
    await context.close().catch(() => {});
    await browser?.close().catch(() => {});
    const recordedPath = await video?.path().catch(() => null);

    // Normalize to a deterministic artifact name (contract: <run>/video.webm).
    if (recordedPath) {
      videoPath = path.join(outputDir, 'video.webm');
      await fs.rename(recordedPath, videoPath).catch(() => {
        videoPath = recordedPath;
      });
    }
  }

  return { sourceDir: outputDir, videoPath, warnings, consoleErrors };
}
