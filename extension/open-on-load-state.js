function isValidTabId(tabId) {
  return Number.isInteger(tabId) && tabId >= 0;
}

export function createOpenOnLoadState() {
  const pending = new Set();

  return {
    requestOpenOnNextLoad(tabId) {
      if (!isValidTabId(tabId)) {
        return false;
      }

      pending.add(tabId);
      return true;
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
