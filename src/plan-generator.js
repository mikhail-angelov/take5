const DEFAULT_VIEWPORT_PRESET = 'desktop-1280';

function cloneAnnotation(annotation) {
  return { ...annotation };
}

function getStepId(step, index) {
  if (typeof step.id === 'string' && step.id.trim()) {
    return step.id;
  }

  return `step-${index + 1}`;
}

function getAnnotationStepRef(annotation) {
  if (typeof annotation.nearestStepId === 'string' && annotation.nearestStepId.trim()) {
    return annotation.nearestStepId;
  }

  if (typeof annotation.stepId === 'string' && annotation.stepId.trim()) {
    return annotation.stepId;
  }

  if (Number.isInteger(annotation.stepIndex) && annotation.stepIndex >= 0) {
    return annotation.stepIndex;
  }

  return null;
}

export function createReplayPlan(bundle) {
  const steps = bundle.steps.map((step, index) => ({
    ...step,
    id: getStepId(step, index),
  }));

  const stepsById = new Map(steps.map((step, index) => [step.id, { step, index }]));

  for (const step of steps) {
    step.annotations = [];
  }

  for (const annotation of bundle.annotations) {
    const stepRef = getAnnotationStepRef(annotation);
    const stepEntry =
      typeof stepRef === 'number'
        ? steps[stepRef] && { step: steps[stepRef], index: stepRef }
        : stepsById.get(stepRef);

    if (!stepEntry) {
      continue;
    }

    stepEntry.step.annotations.push(cloneAnnotation(annotation));
  }

  return {
    version: bundle.metadata.schemaVersion,
    scenarioId: bundle.metadata.scenarioId,
    baseUrl: typeof bundle.metadata.baseUrl === 'string' && bundle.metadata.baseUrl.trim() ? bundle.metadata.baseUrl : null,
    viewportPreset:
      typeof bundle.metadata.viewportPreset === 'string' && bundle.metadata.viewportPreset.trim()
        ? bundle.metadata.viewportPreset
        : DEFAULT_VIEWPORT_PRESET,
    steps: steps.map((step) => {
      if (step.annotations.length === 0) {
        const { annotations, ...rest } = step;
        return rest;
      }

      return step;
    }),
  };
}
