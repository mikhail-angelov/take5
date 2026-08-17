function isValidTabId(tabId) {
  return Number.isInteger(tabId) && tabId >= 0;
}

// Remembers, per tab, whether the user asked for the Take5 panel. The panel is
// hidden everywhere until the extension icon is clicked, and the flag survives
// navigations inside that tab.
export function createToolbarVisibilityState() {
  const visible = new Set();

  return {
    isVisible(tabId) {
      return isValidTabId(tabId) && visible.has(tabId);
    },

    setVisible(tabId, shouldBeVisible) {
      if (!isValidTabId(tabId)) {
        return false;
      }

      if (shouldBeVisible) {
        visible.add(tabId);
      } else {
        visible.delete(tabId);
      }

      return shouldBeVisible;
    },

    toggle(tabId) {
      if (!isValidTabId(tabId)) {
        return false;
      }

      return this.setVisible(tabId, !visible.has(tabId));
    },

    forget(tabId) {
      if (!isValidTabId(tabId)) {
        return false;
      }

      return visible.delete(tabId);
    },
  };
}
