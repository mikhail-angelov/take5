// Narration captions (features 6 + 8). We derive one caption per meaningful plan
// step, optionally rewrite them into natural narration with an LLM (feature 8),
// and hand them to recast's `.subtitles(textFn)` — recast times and chunks them
// against the trace (feature 6), so we never compute SRT timestamps ourselves.

// Plan step types that map 1:1 to a single Playwright action in the trace.
const MEANINGFUL_PLAN_TYPES = new Set(['navigate', 'click', 'fill', 'select', 'keypress']);
export const MIN_CAPTION_DURATION_MS = 3000;

// The matching recast/Playwright action methods, in trace order. A plan `fill`
// becomes a visible `type` action; its preparatory keyboard presses must not
// consume that caption. A planned standalone keypress still has an empty slot
// and consumes its matching `press` action. Callout injection and assertions
// are skipped for the same reason.
const MEANINGFUL_METHODS = new Set(['goto', 'click', 'type', 'selectOption', 'press']);

function prettyUrl(url) {
  try {
    const parsed = new URL(url);
    const tail = `${parsed.host}${parsed.pathname}`.replace(/\/$/, '');
    return tail || url;
  } catch {
    return url;
  }
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function humanizeTarget(selector) {
  if (typeof selector !== 'string' || selector.trim().length === 0) {
    return 'the element';
  }
  // Prefer the visible label from Playwright text selectors (has-text / text=).
  const labeled = selector.match(/has-text\(["']([^"']+)["']\)|text=["']?([^"']+)["']?/i);
  if (labeled) {
    return (labeled[1] || labeled[2]).trim();
  }
  return selector.trim().replace(/^[#.]/, '').replace(/[-_]+/g, ' ');
}

/** Deterministic, human-readable caption for a single plan step (pure). */
export function describeStep(step) {
  const label = isNonEmptyString(step.label) ? step.label.trim() : null;
  switch (step.type) {
    case 'navigate':
      return `Open ${prettyUrl(step.url)}`;
    case 'click':
      return `Click ${label ?? humanizeTarget(step.selector)}`;
    case 'fill':
      return step.value
        ? `Enter “${step.value}”`
        : `Fill in ${label ?? humanizeTarget(step.selector)}`;
    case 'select':
      return `Select “${step.value}”`;
    // Individual keystrokes (e.g. editing code) are too granular to narrate; emit
    // an empty caption so the trace action stays aligned but shows no text.
    case 'keypress':
      return '';
    default:
      return null;
  }
}

/**
 * Ordered caption text for every meaningful plan step, positionally aligned 1:1
 * with the visible trace actions (see createCaptionTextFn). Entries may be
 * empty strings — recast drops those — so alignment must not be filtered away.
 */
export function buildStepCaptions(plan) {
  return (plan.steps ?? [])
    .filter((step) => MEANINGFUL_PLAN_TYPES.has(step.type))
    .map((step) => describeStep(step) ?? '');
}

/**
 * Build a recast `subtitles` text function. It queues the next caption at its
 * visible trace action, then returns it for the dwell immediately following that
 * action. The replay runner records each such dwell for at least three seconds,
 * which makes every displayed subtitle readable and lets the video grow as needed.
 */
export function createCaptionTextFn(captions) {
  let index = 0;
  let pendingCaption = '';
  return (action) => {
    if (action?.method === 'waitForTimeout' && pendingCaption) {
      const text = pendingCaption;
      pendingCaption = '';
      return text;
    }
    if (!MEANINGFUL_METHODS.has(action?.method)) {
      return '';
    }
    if (action.method === 'press' && captions[index] !== '') {
      return '';
    }
    const text = captions[index];
    index += 1;
    pendingCaption = text ?? '';
    return '';
  };
}

/** Build source-trace SRT entries from the caption dwell before each action. */
export function buildCaptionSrtEntries(actions, captions, firstActionStartMs = 0) {
  const entries = [];
  let index = 0;
  let precedingDwell = null;

  for (const action of actions) {
    const durationMs = action.endTime - action.startTime;
    if (action.method === 'waitForTimeout' && durationMs >= MIN_CAPTION_DURATION_MS) {
      precedingDwell = action;
      continue;
    }
    if (!MEANINGFUL_METHODS.has(action.method)) continue;
    if (action.method === 'press' && captions[index] !== '') continue;
    const text = captions[index] ?? '';
    index += 1;
    if (!text || !precedingDwell) continue;
    entries.push({
      index: entries.length + 1,
      startMs: precedingDwell.startTime - firstActionStartMs,
      endMs: precedingDwell.endTime - firstActionStartMs,
      text,
    });
    precedingDwell = null;
  }

  return entries;
}

function llmConfigFromEnv(env = process.env) {
  const apiKey = env.TAKE5_LLM_API_KEY;
  if (!apiKey) {
    return null;
  }
  return {
    apiKey,
    baseURL: env.TAKE5_LLM_BASE_URL || 'https://api.openai.com/v1',
    model: env.TAKE5_LLM_MODEL || 'gpt-4o-mini',
  };
}

/**
 * Rewrite deterministic step captions into friendly narration with a single LLM
 * call (feature 8). Falls back to the input captions on any problem, so the
 * render never breaks because of the optional AI step.
 */
export async function naturalizeCaptions(captions, options = {}) {
  const { env = process.env, fetchImpl = globalThis.fetch, logger = { info() {} } } = options;
  const config = llmConfigFromEnv(env);

  if (captions.length === 0) {
    return captions;
  }
  if (!config) {
    logger.info('ai-captions: TAKE5_LLM_API_KEY not set — using deterministic captions');
    return captions;
  }

  const prompt = [
    'Rewrite each product-demo UI step into a short, friendly narration line.',
    'Keep the same order and count. Return ONLY a JSON array of strings.',
    '',
    JSON.stringify(captions),
  ].join('\n');

  try {
    const response = await fetchImpl(`${config.baseURL}/chat/completions`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        authorization: `Bearer ${config.apiKey}`,
      },
      body: JSON.stringify({
        model: config.model,
        temperature: 0.4,
        messages: [{ role: 'user', content: prompt }],
      }),
    });

    if (!response.ok) {
      throw new Error(`LLM request failed: ${response.status}`);
    }

    const data = await response.json();
    const content = data?.choices?.[0]?.message?.content ?? '';
    const parsed = JSON.parse(content.slice(content.indexOf('['), content.lastIndexOf(']') + 1));

    if (Array.isArray(parsed) && parsed.length === captions.length) {
      return parsed.map((text, i) =>
        typeof text === 'string' && text.trim() ? text.trim() : captions[i],
      );
    }
    throw new Error('LLM returned an unexpected shape');
  } catch (error) {
    logger.info(`ai-captions: falling back to deterministic captions (${error.message})`);
    return captions;
  }
}
