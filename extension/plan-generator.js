const DEFAULT_VIEWPORT_PRESET = 'desktop-1280';

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function normalizeStringField(value) {
  return isNonEmptyString(value) ? value : undefined;
}

function normalizeTargetRect(targetRect) {
  if (!targetRect || typeof targetRect !== 'object' || Array.isArray(targetRect)) {
    return undefined;
  }

  const { x, y, width, height } = targetRect;
  if ([x, y, width, height].some((value) => typeof value !== 'number' || !Number.isFinite(value))) {
    return undefined;
  }

  return { x, y, width, height };
}

function normalizeViewport(viewport) {
  if (!viewport || typeof viewport !== 'object' || Array.isArray(viewport)) {
    return undefined;
  }

  const { width, height } = viewport;
  if (
    typeof width !== 'number' ||
    !Number.isFinite(width) ||
    width <= 0 ||
    typeof height !== 'number' ||
    !Number.isFinite(height) ||
    height <= 0
  ) {
    return undefined;
  }

  return { width, height };
}

function normalizeReplayAnnotation(annotation, orderIndex) {
  const normalized = {
    description: annotation.description,
    orderIndex,
  };

  const selector = normalizeStringField(annotation.selector);
  if (selector) {
    normalized.selector = selector;
  }

  const pageUrl = normalizeStringField(annotation.pageUrl);
  if (pageUrl) {
    normalized.pageUrl = pageUrl;
  }

  const targetRect = normalizeTargetRect(annotation.targetRect);
  if (targetRect) {
    normalized.targetRect = targetRect;
  }

  return normalized;
}

function normalizeReplayStep(step, index) {
  const normalized = {
    id: `step-${index + 1}`,
    type: step.type,
  };

  switch (step.type) {
    case 'navigate':
      normalized.url = step.url;
      break;
    case 'click':
      if (normalizeStringField(step.ref)) {
        normalized.ref = step.ref;
      }
      if (normalizeStringField(step.selector)) {
        normalized.selector = step.selector;
      }
      if (normalizeStringField(step.label)) {
        normalized.label = step.label;
      }
      break;
    case 'fill':
    case 'select':
      if (normalizeStringField(step.ref)) {
        normalized.ref = step.ref;
      }
      if (normalizeStringField(step.selector)) {
        normalized.selector = step.selector;
      }
      if (normalizeStringField(step.label)) {
        normalized.label = step.label;
      }
      normalized.value = step.value;
      if (
        step.type === 'fill' &&
        typeof step.typingDelayMs === 'number' &&
        Number.isFinite(step.typingDelayMs) &&
        step.typingDelayMs >= 0
      ) {
        normalized.typingDelayMs = step.typingDelayMs;
      }
      break;
    case 'keypress':
      normalized.key = step.key;
      break;
    case 'scroll':
      normalized.direction = step.direction;
      normalized.amount = step.amount;
      break;
    case 'wait':
      normalized.ms = step.ms;
      break;
    case 'assert_text':
      if (normalizeStringField(step.ref)) {
        normalized.ref = step.ref;
      }
      if (normalizeStringField(step.selector)) {
        normalized.selector = step.selector;
      }
      normalized.text = step.text;
      break;
    case 'pointer_drag':
      if (normalizeStringField(step.selector)) {
        normalized.selector = step.selector;
      }
      normalized.points = step.points.map((point) => ({ x: point.x, y: point.y, t: point.t }));
      break;
    default:
      break;
  }

  return normalized;
}

function getStepRef(annotation) {
  if (Number.isInteger(annotation.stepIndex) && annotation.stepIndex >= 0) {
    return { kind: 'index', value: annotation.stepIndex };
  }

  const nearestStepId = normalizeStringField(annotation.nearestStepId);
  if (nearestStepId) {
    return { kind: 'id', value: nearestStepId };
  }

  const stepId = normalizeStringField(annotation.stepId);
  if (stepId) {
    return { kind: 'id', value: stepId };
  }

  return null;
}

function countSourceStepIds(steps) {
  const counts = new Map();
  for (const step of steps) {
    const sourceId = normalizeStringField(step.id);
    if (!sourceId) {
      continue;
    }
    counts.set(sourceId, (counts.get(sourceId) || 0) + 1);
  }
  return counts;
}

export function createReplayPlan(bundle) {
  const sourceStepIdCounts = countSourceStepIds(bundle.steps);
  const sourceStepIdToReplayStepId = new Map();
  const steps = bundle.steps.map((step, index) => normalizeReplayStep(step, index));
  const annotations = [];

  for (let index = 0; index < bundle.steps.length; index += 1) {
    const sourceId = normalizeStringField(bundle.steps[index].id);
    if (sourceId && sourceStepIdCounts.get(sourceId) === 1) {
      sourceStepIdToReplayStepId.set(sourceId, steps[index].id);
    }
    steps[index].annotations = [];
  }

  bundle.annotations.forEach((annotation, orderIndex) => {
    const normalizedAnnotation = normalizeReplayAnnotation(annotation, orderIndex);
    const stepRef = getStepRef(annotation);
    let linkedStep = null;

    if (stepRef?.kind === 'index' && steps[stepRef.value]) {
      linkedStep = steps[stepRef.value];
    } else if (stepRef?.kind === 'id') {
      const replayStepId = sourceStepIdToReplayStepId.get(stepRef.value);
      if (replayStepId) {
        linkedStep = steps.find((step) => step.id === replayStepId) || null;
      }
    }

    if (linkedStep) {
      linkedStep.annotations.push(normalizedAnnotation);
      return;
    }

    annotations.push(normalizedAnnotation);
  });

  const plan = {
    version: bundle.metadata.schemaVersion,
    scenarioId: bundle.metadata.scenarioId,
    baseUrl: normalizeStringField(bundle.metadata.baseUrl) || null,
    viewportPreset: normalizeStringField(bundle.metadata.viewportPreset) || DEFAULT_VIEWPORT_PRESET,
    annotations,
    steps,
  };

  const viewport = normalizeViewport(bundle.metadata.viewport);
  if (viewport) {
    plan.viewport = viewport;
  }

  return plan;
}
