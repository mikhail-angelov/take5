function isValidTabId(tabId) {
  return Number.isInteger(tabId) && tabId >= 0;
}

export function createOpenOnLoadState() {
  const enabled = new Set();
  const pending = new Set();

  return {
    enableForTab(tabId) {
      if (!isValidTabId(tabId)) {
        return false;
      }

      enabled.add(tabId);
      pending.add(tabId);
      return true;
    },

    disableForTab(tabId) {
      if (!isValidTabId(tabId)) {
        return false;
      }

      const wasEnabled = enabled.delete(tabId);
      pending.delete(tabId);
      return wasEnabled;
    },

    isEnabledForTab(tabId) {
      return isValidTabId(tabId) && enabled.has(tabId);
    },

    consumePendingOpen(tabId) {
      if (!isValidTabId(tabId) || !pending.has(tabId)) {
        return false;
      }

      pending.delete(tabId);
      return true;
    },
  };
}
