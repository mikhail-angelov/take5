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
