const OVERLAY_ROOT_ID = 'take5-annotation-overlay-root';
const ANNOTATION_MODE_CLASS = 'take5-annotation-mode';

function isElementLike(value) {
  return value && typeof value === 'object' && typeof value.getBoundingClientRect === 'function';
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function cloneRect(rect) {
  if (!rect) {
    return null;
  }

  return {
    x: rect.x,
    y: rect.y,
    width: rect.width,
    height: rect.height,
  };
}

function resolveSelector(document, annotation) {
  if (isNonEmptyString(annotation?.selector)) {
    return annotation.selector.trim();
  }

  if (isNonEmptyString(annotation?.ref)) {
    return annotation.ref.trim();
  }

  return null;
}

function resolveTargetRect(document, annotation, selector) {
  if (annotation?.targetRect && typeof annotation.targetRect === 'object') {
    return cloneRect(annotation.targetRect);
  }

  if (!selector || typeof document?.querySelector !== 'function') {
    return null;
  }

  const element = document.querySelector(selector);
  if (!isElementLike(element)) {
    return null;
  }

  const rect = element.getBoundingClientRect();
  return rect ? cloneRect(rect) : null;
}

function ensureOverlayRoot(document) {
  const existing = document.getElementById?.(OVERLAY_ROOT_ID);
  if (existing) {
    return existing;
  }

  const root = document.createElement('div');
  root.id = OVERLAY_ROOT_ID;
  root.dataset.take5OverlayRoot = 'true';
  root.style.position = 'fixed';
  root.style.inset = '0';
  root.style.pointerEvents = 'none';
  root.style.zIndex = '2147483647';
  root.style.fontFamily = 'system-ui, sans-serif';
  document.body.appendChild(root);
  return root;
}

function createMarkerElement(document, annotation, rect) {
  const marker = document.createElement('div');
  marker.className = 'take5-annotation-marker';
  marker.textContent = 'Note';
  marker.title = annotation.description;
  marker.dataset.take5Annotation = 'true';
  marker.style.position = 'fixed';
  marker.style.pointerEvents = 'none';
  marker.style.background = '#ff5c00';
  marker.style.color = '#fff';
  marker.style.border = '2px solid #fff';
  marker.style.borderRadius = '999px';
  marker.style.padding = '4px 8px';
  marker.style.fontSize = '12px';
  marker.style.fontWeight = '700';
  marker.style.boxShadow = '0 6px 18px rgba(0, 0, 0, 0.24)';

  if (rect) {
    const left = Math.max(8, rect.x);
    const top = Math.max(8, rect.y - 30);
    marker.style.left = `${left}px`;
    marker.style.top = `${top}px`;
  } else {
    marker.style.left = '16px';
    marker.style.top = '16px';
  }

  return marker;
}

function createHighlightBox(document, rect, label) {
  const box = document.createElement('div');
  box.className = 'take5-step-highlight';
  box.dataset.take5Highlight = 'true';
  box.style.position = 'fixed';
  box.style.pointerEvents = 'none';
  box.style.border = '3px solid #38bdf8';
  box.style.borderRadius = '10px';
  box.style.boxShadow = '0 0 0 4px rgba(56, 189, 248, 0.18)';
  box.style.left = `${Math.max(0, rect.x - 6)}px`;
  box.style.top = `${Math.max(0, rect.y - 6)}px`;
  box.style.width = `${Math.max(0, rect.width + 12)}px`;
  box.style.height = `${Math.max(0, rect.height + 12)}px`;

  if (label) {
    const caption = document.createElement('div');
    caption.textContent = label;
    caption.style.position = 'absolute';
    caption.style.left = '0';
    caption.style.top = '-28px';
    caption.style.background = '#0f172a';
    caption.style.color = '#fff';
    caption.style.padding = '4px 8px';
    caption.style.borderRadius = '999px';
    caption.style.fontSize = '12px';
    caption.style.whiteSpace = 'nowrap';
    box.appendChild(caption);
  }

  return box;
}

function createPromptOverlay(document) {
  const dialog = document.createElement('div');
  dialog.className = 'take5-annotation-dialog';
  dialog.dataset.take5Prompt = 'true';
  dialog.style.position = 'fixed';
  dialog.style.inset = '0';
  dialog.style.display = 'flex';
  dialog.style.alignItems = 'center';
  dialog.style.justifyContent = 'center';
  dialog.style.background = 'rgba(15, 23, 42, 0.22)';
  dialog.style.pointerEvents = 'auto';

  const panel = document.createElement('div');
  panel.dataset.take5PromptPanel = 'true';
  panel.style.width = '320px';
  panel.style.background = '#0f172a';
  panel.style.color = '#fff';
  panel.style.borderRadius = '16px';
  panel.style.padding = '16px';
  panel.style.boxShadow = '0 18px 50px rgba(15, 23, 42, 0.35)';
  panel.style.pointerEvents = 'auto';

  const title = document.createElement('div');
  title.textContent = 'Add annotation';
  title.style.fontSize = '14px';
  title.style.fontWeight = '700';
  title.style.marginBottom = '12px';

  const input = document.createElement('textarea');
  input.rows = 4;
  input.placeholder = 'Describe what matters here';
  input.style.width = '100%';
  input.style.boxSizing = 'border-box';
  input.style.borderRadius = '10px';
  input.style.border = '1px solid rgba(148, 163, 184, 0.35)';
  input.style.padding = '10px';
  input.style.resize = 'vertical';
  input.style.marginBottom = '12px';

  const actions = document.createElement('div');
  actions.style.display = 'flex';
  actions.style.justifyContent = 'flex-end';
  actions.style.gap = '8px';

  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.textContent = 'Cancel';

  const save = document.createElement('button');
  save.type = 'button';
  save.textContent = 'Save';

  actions.append(cancel, save);
  panel.append(title, input, actions);
  dialog.appendChild(panel);

  return { dialog, panel, input, save, cancel };
}

export function createPromptInteractionController({ dialog, panel, input, save, cancel }) {
  return new Promise((resolve) => {
    let settled = false;

    const finish = (value) => {
      if (settled) {
        return;
      }

      settled = true;
      dialog.remove();
      resolve(isNonEmptyString(value) ? value.trim() : null);
    };

    save.addEventListener('click', () => finish(input.value));
    cancel.addEventListener('click', () => finish(null));
    dialog.addEventListener('click', (event) => {
      if (event.target === dialog) {
        finish(null);
      }
    });
    dialog.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        finish(null);
      }
      if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        finish(input.value);
      }
    });
    panel.addEventListener('click', (event) => {
      event.stopPropagation?.();
    });
  });
}

export function createAnnotationOverlay(document = globalThis.document) {
  if (!document?.body) {
    return {
      clear() {},
      setAnnotationMode() {},
      promptForAnnotation() {
        return Promise.resolve(null);
      },
      pinAnnotation() {},
      showAnnotations() {},
      showStep() {},
      setReplayMode() {},
    };
  }

  const overlayRoot = ensureOverlayRoot(document);

  function clear() {
    overlayRoot.querySelectorAll('[data-take5-overlay="true"]').forEach((node) => node.remove());
  }

  function setAnnotationMode(enabled) {
    document.body.classList.toggle(ANNOTATION_MODE_CLASS, enabled);
    document.body.style.cursor = enabled ? 'crosshair' : '';
  }

  function promptForAnnotation(target, options = {}) {
    const { defaultValue = '' } = options;
    const { dialog, panel, input, save, cancel } = createPromptOverlay(document);
    input.value = defaultValue;
    overlayRoot.appendChild(dialog);
    input.focus();
    input.select?.();

    return createPromptInteractionController({ dialog, panel, input, save, cancel });
  }

  function pinAnnotation(annotation) {
    const selector = resolveSelector(document, annotation);
    const rect = resolveTargetRect(document, annotation, selector);
    const marker = createMarkerElement(document, annotation, rect);
    marker.dataset.take5Overlay = 'true';
    marker.dataset.take5AnnotationId = annotation.orderIndex != null ? String(annotation.orderIndex) : '';
    overlayRoot.appendChild(marker);
    return marker;
  }

  function showAnnotations(annotations = []) {
    overlayRoot.querySelectorAll('.take5-annotation-marker').forEach((node) => node.remove());
    for (const annotation of annotations) {
      pinAnnotation(annotation);
    }
  }

  function showStep(step) {
    overlayRoot.querySelectorAll('.take5-step-highlight').forEach((node) => node.remove());
    if (!step) {
      return null;
    }

    const selector = isNonEmptyString(step.selector) ? step.selector : isNonEmptyString(step.ref) ? step.ref : null;
    const target = selector && typeof document.querySelector === 'function' ? document.querySelector(selector) : null;
    const rect = target && isElementLike(target) ? target.getBoundingClientRect() : null;
    if (!rect) {
      return null;
    }

    const label = `${step.type}`;
    const highlight = createHighlightBox(document, cloneRect(rect), label);
    highlight.dataset.take5Overlay = 'true';
    overlayRoot.appendChild(highlight);
    return highlight;
  }

  function setReplayMode(mode) {
    overlayRoot.dataset.take5ReplayMode = mode;
  }

  return {
    clear,
    setAnnotationMode,
    promptForAnnotation,
    pinAnnotation,
    showAnnotations,
    showStep,
    setReplayMode,
  };
}
