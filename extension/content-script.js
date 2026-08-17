const TOOLBAR_FRAME_ID = 'take5-toolbar-frame';
const TOGGLE_MESSAGE_TYPE = 'take5:toolbar-toggle';
const RESIZE_MESSAGE_TYPE = 'take5:toolbar-resize';
const DRAG_START_MESSAGE_TYPE = 'take5:toolbar-drag-start';
const COMMAND_MESSAGE_TYPE = 'take5:command';

// Collapsed height is a fallback; the toolbar reports its bar's real height so
// the frame wraps it tightly.
const COLLAPSED_SIZE = { width: 500, height: 48 };
const EXPANDED_SIZE = { width: 500, height: 640 };
const DEFAULT_TYPING_DELAY_MS = 45;

function log(...args) {
  try {
    console.log('[Take5:content]', ...args);
  } catch {
    // console may be unavailable in some contexts.
  }
}

function applyFrameSize(frame, size) {
  frame.style.width = `${size.width}px`;
  frame.style.height = `${size.height}px`;
}

// Let the user drag the frame by its bar. The bar's mousedown (inside the
// iframe) hands off here; we track the pointer on the top document so the drag
// keeps working even when the cursor leaves the frame. `grabX/grabY` is the
// pointer's offset from the frame's top-left corner (equal to its coordinates
// within the iframe).
function beginFrameDrag(frame, grabX, grabY) {
  const rect = frame.getBoundingClientRect();

  // Switch from the centered layout (left:50% + translateX(-50%)) to absolute
  // pixel positioning so the frame follows the pointer directly.
  frame.style.left = `${rect.left}px`;
  frame.style.top = `${rect.top}px`;
  frame.style.transform = 'none';
  // The frame must not swallow the move/up events while dragging.
  frame.style.pointerEvents = 'none';
  // Avoid selecting page text under the moving cursor.
  const previousUserSelect = document.body?.style.userSelect ?? '';
  if (document.body) {
    document.body.style.userSelect = 'none';
  }

  const onMove = (event) => {
    const maxLeft = Math.max(0, window.innerWidth - rect.width);
    const maxTop = Math.max(0, window.innerHeight - rect.height);
    const left = Math.min(Math.max(0, event.clientX - grabX), maxLeft);
    const top = Math.min(Math.max(0, event.clientY - grabY), maxTop);
    frame.style.left = `${left}px`;
    frame.style.top = `${top}px`;
  };

  const onUp = () => {
    window.removeEventListener('mousemove', onMove, true);
    window.removeEventListener('mouseup', onUp, true);
    frame.style.pointerEvents = '';
    if (document.body) {
      document.body.style.userSelect = previousUserSelect;
    }
  };

  window.addEventListener('mousemove', onMove, true);
  window.addEventListener('mouseup', onUp, true);
}

// Mirrors extension/toolbar-visibility.js, which is unit-tested; the content
// script stays a classic script and cannot import it.
function createToolbarVisibilityController({ frame, onChange }) {
  return {
    show() {
      frame.style.display = 'block';
      onChange(true);
    },
    hide() {
      frame.style.display = 'none';
      onChange(false);
    },
    isVisible() {
      return frame.style.display !== 'none';
    },
  };
}

function injectToolbar() {
  const existing = document.getElementById(TOOLBAR_FRAME_ID);
  if (existing) {
    return {
      frame: existing,
      visibility: createToolbarVisibilityController({
        frame: existing,
        onChange: reportToolbarVisibility,
      }),
    };
  }

  const frame = document.createElement('iframe');
  frame.id = TOOLBAR_FRAME_ID;
  frame.title = 'Take5 toolbar';
  frame.src = chrome.runtime.getURL('toolbar.html');
  frame.setAttribute('aria-label', 'Take5 toolbar');
  frame.style.position = 'fixed';
  frame.style.left = '50%';
  frame.style.top = '12px';
  frame.style.transform = 'translateX(-50%)';
  applyFrameSize(frame, COLLAPSED_SIZE);
  frame.style.border = '0';
  frame.style.zIndex = '2147483647';
  frame.style.background = 'transparent';
  frame.style.colorScheme = 'normal';
  frame.style.overflow = 'hidden';
  // Hidden until the user clicks the extension icon.
  frame.style.display = 'none';
  const visibility = createToolbarVisibilityController({
    frame,
    onChange: reportToolbarVisibility,
  });

  window.addEventListener('message', (event) => {
    if (event.source !== frame.contentWindow) {
      return;
    }

    if (event.data?.type === TOGGLE_MESSAGE_TYPE) {
      visibility.hide();
      return;
    }

    if (event.data?.type === DRAG_START_MESSAGE_TYPE) {
      beginFrameDrag(frame, event.data.x, event.data.y);
      return;
    }

    if (event.data?.type === RESIZE_MESSAGE_TYPE) {
      const base = event.data.expanded ? EXPANDED_SIZE : COLLAPSED_SIZE;
      const height =
        typeof event.data.height === 'number' && event.data.height > 0
          ? event.data.height
          : base.height;
      applyFrameSize(frame, { width: base.width, height });
    }
  });

  const root = document.body || document.documentElement;
  root.appendChild(frame);
  return { frame, visibility };
}

function getToolbarFrame() {
  return document.getElementById(TOOLBAR_FRAME_ID);
}

function postToToolbar(message) {
  const frame = getToolbarFrame();
  if (!frame?.contentWindow) {
    return;
  }

  frame.contentWindow.postMessage(message, '*');
}

function postStatus(mode, message) {
  postToToolbar({
    type: 'take5:status',
    mode,
    message,
  });
}

// Keep the background's per-tab flag in sync so the extension icon always
// toggles from the state the user actually sees.
function reportToolbarVisibility(visible) {
  sendRuntimeMessage({
    type: 'take5:toolbar-visibility',
    payload: { visible },
  }).catch((error) => {
    log('toolbar-visibility sync failed:', error.message);
  });
}

function syncCaptureToBackground(bundle, capturing) {
  sendRuntimeMessage({
    type: 'take5:capture-sync',
    payload: { bundle, capturing },
  }).catch((error) => {
    // The service worker may be briefly unavailable; the next step re-syncs.
    log('capture-sync to background failed:', error.message);
  });
}

function sendRuntimeMessage(message) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage(message, (response) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }

      resolve(response ?? null);
    });
  });
}

function createReplayPointerEvent(type, point, buttons) {
  const init = {
    bubbles: true,
    cancelable: true,
    clientX: point.x,
    clientY: point.y,
    button: 0,
    buttons,
    pointerId: 1,
    pointerType: 'mouse',
    isPrimary: true,
  };
  if (typeof PointerEvent === 'function') {
    return new PointerEvent(type, init);
  }
  return new MouseEvent(type.replace('pointer', 'mouse'), init);
}

async function replayPointerDrag(document, step, capturedViewport = null) {
  const points = step.points ?? [];
  if (points.length < 2) {
    throw new Error('pointer_drag requires at least two points');
  }

  const scaleX = capturedViewport?.width ? window.innerWidth / capturedViewport.width : 1;
  const scaleY = capturedViewport?.height ? window.innerHeight / capturedViewport.height : 1;
  const scaledPoints = points.map((point) => ({
    ...point,
    x: point.x * scaleX,
    y: point.y * scaleY,
  }));
  const target = step.selector ? document.querySelector(step.selector) : null;
  const dispatch = (type, point, buttons) =>
    (target ?? document.elementFromPoint?.(point.x, point.y) ?? document).dispatchEvent(
      createReplayPointerEvent(type, point, buttons),
    );

  dispatch('pointerdown', scaledPoints[0], 1);
  let previousTime = scaledPoints[0].t;
  for (const point of scaledPoints.slice(1)) {
    const delay = Math.max(0, point.t - previousTime);
    if (delay > 0) {
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
    dispatch('pointermove', point, 1);
    previousTime = point.t;
  }
  dispatch('pointerup', scaledPoints.at(-1), 0);
}

async function replayFill(target, value, typingDelayMs = DEFAULT_TYPING_DELAY_MS) {
  target.focus?.();
  target.value = '';
  target.dispatchEvent?.(new Event('input', { bubbles: true }));

  for (const character of String(value ?? '')) {
    target.value += character;
    target.dispatchEvent?.(new Event('input', { bubbles: true }));
    if (typingDelayMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, typingDelayMs));
    }
  }

  target.dispatchEvent?.(new Event('change', { bubbles: true }));
}

async function bootstrap() {
  log('bootstrap start at', window.location.href);
  const toolbar = injectToolbar();
  log('toolbar injected');

  const [{ createAnnotationOverlay }, { createCaptureEngine }, { createReplayEngine }] =
    await Promise.all([
      import(chrome.runtime.getURL('annotation-overlay.js')),
      import(chrome.runtime.getURL('capture-engine.js')),
      import(chrome.runtime.getURL('replay-engine.js')),
    ]);
  log('modules imported (overlay, capture, replay)');

  const overlay = createAnnotationOverlay(document);
  const captureEngine = createCaptureEngine({
    document,
    window,
    overlay,
    baseUrl: window.location.href,
    name: document.title,
    onUpdate: (bundle) => {
      postToToolbar({ type: 'take5:capture-state', bundle });
      syncCaptureToBackground(bundle, true);
    },
  });
  const replayEngine = createReplayEngine({
    overlay,
    runStep: async (step, context) => {
      const selector = step.selector ?? step.ref ?? null;
      const target = selector ? document.querySelector(selector) : null;

      switch (step.type) {
        case 'navigate':
          if (step.url && window.location.href !== step.url) {
            window.location.href = step.url;
          }
          break;
        case 'click':
          target?.click?.();
          break;
        case 'fill':
          if (target) {
            await replayFill(target, step.value, step.typingDelayMs ?? DEFAULT_TYPING_DELAY_MS);
          }
          break;
        case 'select':
          if (target) {
            target.value = step.value ?? '';
            target.dispatchEvent?.(new Event('change', { bubbles: true }));
          }
          break;
        case 'keypress':
          document.activeElement?.dispatchEvent?.(
            new KeyboardEvent('keydown', { key: step.key, bubbles: true }),
          );
          break;
        case 'scroll':
          window.scrollBy?.(0, step.direction === 'down' ? step.amount : -step.amount);
          break;
        case 'wait':
          await new Promise((resolve) => setTimeout(resolve, step.ms));
          break;
        case 'assert_text':
          if (!target || !String(target.textContent ?? '').includes(step.text)) {
            throw new Error(`assert_text failed for ${selector ?? step.ref ?? 'target'}`);
          }
          break;
        case 'pointer_drag':
          await replayPointerDrag(document, step, context.plan?.viewport);
          break;
        default:
          break;
      }
    },
  });

  window.addEventListener('keydown', async (event) => {
    if (replayEngine.getState().mode !== 'step') {
      return;
    }

    if (event.key === 'ArrowRight') {
      event.preventDefault();
      await replayEngine.next();
      postStatus('replaying', 'Advanced one step.');
      return;
    }

    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      await replayEngine.back();
      postStatus('replaying', 'Moved back one step.');
    }
  });

  async function handleReplayRequest(payload = {}) {
    const bundle = payload.bundle ?? captureEngine.getBundle?.() ?? null;
    if (!bundle) {
      postStatus('idle', 'Pick a scenario or start a capture first.');
      return;
    }

    await replayEngine.load(bundle);
    const viewport = bundle.metadata?.viewport;
    const viewportDiffers =
      viewport && (viewport.width !== window.innerWidth || viewport.height !== window.innerHeight);

    if (payload.mode === 'step') {
      postStatus(
        'replaying',
        viewportDiffers
          ? 'Viewport differs; pointer drags will be scaled to this window.'
          : 'Step-through replay ready. Use the Next/Back keyboard shortcuts.',
      );
      return;
    }

    postStatus(
      'replaying',
      viewportDiffers
        ? 'Replaying scenario; pointer drags are scaled to this window.'
        : 'Replaying scenario.',
    );
    await replayEngine.play();
    postStatus('idle', 'Replay finished.');
  }

  window.addEventListener('message', async (event) => {
    const message = event.data;

    // A freshly (re)loaded toolbar asks for the current capture so it can show
    // live progress immediately, e.g. after resuming across a navigation.
    if (message?.type === 'take5:request-state') {
      if (captureEngine.isCapturing()) {
        postToToolbar({ type: 'take5:capture-state', bundle: captureEngine.getBundle() });
        postStatus('capturing', 'Capturing.');
      }
      return;
    }

    if (!message || message.type !== COMMAND_MESSAGE_TYPE) {
      return;
    }

    log('command received:', message.command);
    try {
      switch (message.command) {
        case 'capture:start': {
          const bundle = captureEngine.startCapture({
            scenarioId: message.payload?.scenarioId,
            name: message.payload?.name ?? document.title,
            baseUrl: message.payload?.baseUrl ?? window.location.href,
            viewportPreset: message.payload?.viewportPreset,
          });
          postStatus('capturing', 'Capture started.');
          postToToolbar({ type: 'take5:capture-state', bundle });
          break;
        }
        case 'capture:stop': {
          const bundle = captureEngine.stopCapture();
          postStatus('idle', 'Capture stopped.');
          postToToolbar({ type: 'take5:capture-state', bundle });
          syncCaptureToBackground(bundle, false);
          break;
        }
        case 'replay:start':
          await handleReplayRequest(message.payload);
          break;
        case 'replay:next':
          await replayEngine.next();
          postStatus('replaying', 'Advanced one step.');
          break;
        case 'replay:back':
          await replayEngine.back();
          postStatus('replaying', 'Moved back one step.');
          break;
        case 'replay:stop':
          await replayEngine.stop();
          postStatus('idle', 'Replay stopped.');
          break;
        default:
          break;
      }
    } catch (error) {
      postStatus('idle', error.message);
    }
  });

  chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (message?.type === 'take5:set-toolbar-visible') {
      if (message.visible) {
        toolbar?.visibility?.show();
      } else {
        toolbar?.visibility?.hide();
      }
      sendResponse({ ok: true });
      return true;
    }

    if (!message?.type) {
      return false;
    }

    return false;
  });

  try {
    const response = await sendRuntimeMessage({ type: 'take5:content-script-ready' });
    log(
      'content-script-ready response:',
      JSON.stringify({
        showToolbar: response?.showToolbar,
        capturing: response?.session?.capturing,
        steps: response?.session?.bundle?.steps?.length,
      }),
    );
    if (response?.ok && response.showToolbar) {
      toolbar?.visibility?.show();
    }

    // Resume an in-progress capture that survived a full page navigation.
    if (response?.session?.capturing && response.session.bundle) {
      log('resuming capture after navigation');
      captureEngine.resumeCapture(response.session.bundle);
      toolbar?.visibility?.show();
      postStatus('capturing', 'Capture resumed after navigation.');
    }
  } catch (error) {
    log('content-script-ready failed:', error.message);
  }

  if (!captureEngine.isCapturing()) {
    postStatus('idle', 'Ready.');
  }
}

if (typeof document !== 'undefined' && globalThis.chrome?.runtime) {
  log('content script injected (build: logging-1)');
  bootstrap().catch((error) => {
    log('bootstrap failed:', error.message, error.stack);
    postStatus('idle', error.message);
  });
}
