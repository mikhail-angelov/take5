const TOOLBAR_FRAME_ID = 'take5-toolbar-frame';
const TOOLBAR_LAUNCHER_ID = 'take5-toolbar-launcher';
const TOGGLE_MESSAGE_TYPE = 'take5:toolbar-toggle';
const COMMAND_MESSAGE_TYPE = 'take5:command';

export function createToolbarVisibilityController({ frame, launcher }) {
  return {
    show() {
      frame.style.display = 'block';
      launcher.style.display = 'none';
    },
    hide() {
      frame.style.display = 'none';
      launcher.style.display = 'block';
    },
  };
}

function injectToolbar() {
  if (document.getElementById(TOOLBAR_FRAME_ID) || document.getElementById(TOOLBAR_LAUNCHER_ID)) {
    return {
      frame: document.getElementById(TOOLBAR_FRAME_ID),
      launcher: document.getElementById(TOOLBAR_LAUNCHER_ID),
      visibility: createToolbarVisibilityController({
        frame: document.getElementById(TOOLBAR_FRAME_ID),
        launcher: document.getElementById(TOOLBAR_LAUNCHER_ID),
      }),
    };
  }

  const launcher = document.createElement('button');
  launcher.id = TOOLBAR_LAUNCHER_ID;
  launcher.type = 'button';
  launcher.textContent = 'Take5';
  launcher.setAttribute('aria-label', 'Show Take5 toolbar');
  launcher.style.position = 'fixed';
  launcher.style.right = '18px';
  launcher.style.bottom = '18px';
  launcher.style.zIndex = '2147483647';
  launcher.style.display = 'none';
  launcher.style.border = '0';
  launcher.style.borderRadius = '999px';
  launcher.style.padding = '10px 14px';
  launcher.style.background = 'linear-gradient(135deg, #38bdf8, #0ea5e9)';
  launcher.style.color = '#04111f';
  launcher.style.font = '600 13px/1.1 system-ui, sans-serif';
  launcher.style.boxShadow = '0 14px 40px rgba(2, 6, 23, 0.3)';
  launcher.style.cursor = 'pointer';

  const frame = document.createElement('iframe');
  frame.id = TOOLBAR_FRAME_ID;
  frame.title = 'Take5 toolbar';
  frame.src = chrome.runtime.getURL('toolbar.html');
  frame.setAttribute('aria-label', 'Take5 toolbar');
  frame.style.position = 'fixed';
  frame.style.right = '16px';
  frame.style.bottom = '16px';
  frame.style.width = '360px';
  frame.style.height = '560px';
  frame.style.border = '0';
  frame.style.zIndex = '2147483647';
  frame.style.background = 'transparent';
  frame.style.boxShadow = '0 18px 50px rgba(15, 23, 42, 0.28)';
  frame.style.borderRadius = '18px';
  frame.style.overflow = 'hidden';
  const visibility = createToolbarVisibilityController({ frame, launcher });

  launcher.addEventListener('click', () => {
    visibility.show();
  });

  window.addEventListener('message', (event) => {
    if (event.source !== frame.contentWindow || event.data?.type !== TOGGLE_MESSAGE_TYPE) {
      return;
    }

    visibility.hide();
  });

  const root = document.body || document.documentElement;
  root.appendChild(launcher);
  root.appendChild(frame);
  return { frame, launcher, visibility };
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

async function bootstrap() {
  const toolbar = injectToolbar();

  const [{ createAnnotationOverlay }, { createCaptureEngine }, { createReplayEngine }] = await Promise.all([
    import(chrome.runtime.getURL('annotation-overlay.js')),
    import(chrome.runtime.getURL('capture-engine.js')),
    import(chrome.runtime.getURL('replay-engine.js')),
  ]);

  const overlay = createAnnotationOverlay(document);
  const captureEngine = createCaptureEngine({
    document,
    window,
    overlay,
    baseUrl: window.location.href,
    name: document.title,
  });
  const replayEngine = createReplayEngine({
    overlay,
    runStep: async (step) => {
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
            target.focus?.();
            target.value = step.value ?? '';
            target.dispatchEvent?.(new Event('input', { bubbles: true }));
            target.dispatchEvent?.(new Event('change', { bubbles: true }));
          }
          break;
        case 'select':
          if (target) {
            target.value = step.value ?? '';
            target.dispatchEvent?.(new Event('change', { bubbles: true }));
          }
          break;
        case 'keypress':
          document.activeElement?.dispatchEvent?.(new KeyboardEvent('keydown', { key: step.key, bubbles: true }));
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

    if (payload.mode === 'step') {
      postStatus('replaying', 'Step-through replay ready. Use the Next/Back keyboard shortcuts.');
      return;
    }

    postStatus('replaying', 'Replaying scenario.');
    await replayEngine.play();
    postStatus('idle', 'Replay finished.');
  }

  window.addEventListener('message', async (event) => {
    const message = event.data;
    if (!message || message.type !== COMMAND_MESSAGE_TYPE) {
      return;
    }

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
    if (message?.type !== 'take5:show-toolbar') {
      return false;
    }

    toolbar?.visibility?.show();
    sendResponse({ ok: true });
    return true;
  });

  postStatus('idle', 'Ready.');
}

if (typeof document !== 'undefined' && globalThis.chrome?.runtime) {
  bootstrap().catch((error) => {
    postStatus('idle', error.message);
  });
}
