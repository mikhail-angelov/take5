export function createToolbarVisibilityController({ frame, onChange = () => {} }) {
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
