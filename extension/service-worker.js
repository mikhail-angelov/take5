import { createScenarioStore } from './scenario-store.js';

const store = createScenarioStore();

async function handleMessage(message) {
  switch (message?.type) {
    case 'take5:list-scenarios':
      return { scenarios: await store.listScenarios() };
    case 'take5:save-scenario':
      return { scenario: await store.saveScenario(message.payload) };
    case 'take5:delete-scenario':
      return { deleted: await store.deleteScenario(message.payload?.id) };
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

  try {
    await chrome.tabs.sendMessage(tab.id, { type: 'take5:show-toolbar' });
  } catch {
    // Ignore unsupported pages like chrome:// where content scripts cannot run.
  }
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  const handledTypes = new Set([
    'take5:list-scenarios',
    'take5:save-scenario',
    'take5:delete-scenario',
  ]);

  if (!handledTypes.has(message?.type)) {
    return false;
  }

  handleMessage(message)
    .then((result) => sendResponse({ ok: true, ...result }))
    .catch((error) => sendResponse({ ok: false, error: error.message }));

  return true;
});
