const KEY_PREFIX = 'take5:capture:';

function sessionKey(tabId) {
  return `${KEY_PREFIX}${tabId}`;
}

/**
 * Persists the active capture bundle per tab so a capture survives full page
 * navigations and reloads, which destroy the content script and its in-memory
 * state. Backed by chrome.storage.session: kept in memory across service-worker
 * restarts and cleared automatically when the browser closes.
 */
export function createCaptureSessionStore(storage) {
  const area = storage ?? globalThis.chrome?.storage?.session;

  return {
    async get(tabId) {
      if (tabId == null || !area) {
        return null;
      }

      const key = sessionKey(tabId);
      const data = await area.get(key);
      return data?.[key] ?? null;
    },

    async set(tabId, session) {
      if (tabId == null || !area) {
        return;
      }

      await area.set({ [sessionKey(tabId)]: session });
    },

    async clear(tabId) {
      if (tabId == null || !area) {
        return;
      }

      await area.remove(sessionKey(tabId));
    },
  };
}
