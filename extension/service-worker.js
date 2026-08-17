import { createScenarioStore } from './scenario-store.js';
import { createToolbarVisibilityState } from './toolbar-visibility-state.js';
import { createCaptureSessionStore } from './capture-session-store.js';

const store = createScenarioStore();
const toolbarVisibility = createToolbarVisibilityState();
const captureSessions = createCaptureSessionStore();

function log(...args) {
  try {
    console.log('[Take5:sw]', ...args);
  } catch {
    // console may be unavailable in some contexts.
  }
}

async function handleMessage(message, sender) {
  const tabId = sender?.tab?.id ?? null;

  switch (message?.type) {
    case 'take5:list-scenarios':
      return { scenarios: await store.listScenarios() };
    case 'take5:save-scenario':
      return { scenario: await store.saveScenario(message.payload) };
    case 'take5:delete-scenario':
      return { deleted: await store.deleteScenario(message.payload?.id) };
    case 'take5:toolbar-visibility':
      toolbarVisibility.setVisible(tabId, Boolean(message.payload?.visible));
      return { visible: toolbarVisibility.isVisible(tabId) };
    case 'take5:capture-sync': {
      const capturing = Boolean(message.payload?.capturing);
      log(
        'capture-sync tab',
        tabId,
        'capturing',
        capturing,
        'steps',
        message.payload?.bundle?.steps?.length,
      );
      await captureSessions.set(tabId, {
        capturing,
        bundle: message.payload?.bundle ?? null,
      });
      return { synced: true };
    }
    default:
      return null;
  }
}

chrome.runtime.onInstalled.addListener(() => {
  chrome.action?.setBadgeText?.({ text: 'T5' });
});

chrome.action.onClicked.addListener(async (tab) => {
  if (!tab?.id) {
    return;
  }

  const visible = toolbarVisibility.toggle(tab.id);
  log('action clicked on tab', tab.id, 'visible', visible);

  try {
    await chrome.tabs.sendMessage(tab.id, { type: 'take5:set-toolbar-visible', visible });
  } catch {
    // No content script yet (page loaded before the extension was installed or
    // reloaded). Inject it now; its ready handshake picks up the flag above.
    try {
      await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        files: ['content-script.js'],
      });
    } catch {
      // Unsupported pages like chrome:// where content scripts cannot run.
      toolbarVisibility.forget(tab.id);
    }
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  toolbarVisibility.forget(tabId);
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  const handledTypes = new Set([
    'take5:list-scenarios',
    'take5:save-scenario',
    'take5:delete-scenario',
    'take5:capture-sync',
    'take5:toolbar-visibility',
    'take5:content-script-ready',
  ]);

  if (!handledTypes.has(message?.type)) {
    return false;
  }

  if (message?.type === 'take5:content-script-ready') {
    const tabId = sender.tab?.id ?? null;
    log('content-script-ready from tab', tabId);
    captureSessions
      .get(tabId)
      .then((session) =>
        sendResponse({
          ok: true,
          showToolbar: toolbarVisibility.isVisible(tabId),
          session,
        }),
      )
      .catch(() => sendResponse({ ok: true, showToolbar: false, session: null }));
    return true;
  }

  handleMessage(message, sender)
    .then((result) => sendResponse({ ok: true, ...result }))
    .catch((error) => sendResponse({ ok: false, error: error.message }));

  return true;
});
