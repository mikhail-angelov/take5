import { Recast as DefaultRecast } from 'playwright-recast';
import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import {
  buildCaptionSrtEntries,
  createCaptionTextFn,
  MIN_CAPTION_DURATION_MS,
} from './captions.js';

// Keep a brief real-time beat around each user action; everything further from an
// action than this is "transit" (LLM builds, code runs, network settles) and gets
// fast-forwarded.
const GAP_HOLD_MS = 700;
const CAPTION_METHODS = new Set(['goto', 'click', 'type', 'selectOption', 'press']);

function writeCaptionSrt(entries) {
  const formatTime = (ms) => {
    const totalSeconds = Math.floor(ms / 1000);
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    const millis = Math.round(ms % 1000);
    return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(
      seconds,
    ).padStart(2, '0')},${String(millis).padStart(3, '0')}`;
  };
  return entries
    .map(
      (entry) =>
        `${entry.index}\n${formatTime(entry.startMs)} --> ${formatTime(entry.endMs)}\n${entry.text}`,
    )
    .join('\n\n')
    .concat('\n');
}

/**
 * Build explicit input zooms for the subtitle sequence. `autoZoom` in recast
 * compares source-trace action times with speed-remapped subtitle times, so a
 * sped-up recording can miss the input window entirely. A preceding click on
 * the same selector provides the field's on-screen position; the subtitle
 * stage provides the correctly remapped time window.
 */
export function buildInputZoomReport(actions, viewport, captions) {
  const report = [];
  let captionIndex = 0;

  for (let index = 0; index < actions.length; index += 1) {
    const action = actions[index];
    if (!CAPTION_METHODS.has(action?.method)) {
      continue;
    }
    if (action.method === 'press' && captions[captionIndex] !== '') {
      continue;
    }

    const caption = captions[captionIndex] ?? '';
    captionIndex += 1;
    if (!caption) {
      continue;
    }

    let zoom = null;
    if (action.method === 'type' && viewport?.width > 0 && viewport?.height > 0) {
      const sameTargetPoint = actions
        .slice(0, index)
        .reverse()
        .find(
          (candidate) =>
            candidate?.point && candidate?.params?.selector === action?.params?.selector,
        )?.point;
      if (sameTargetPoint) {
        zoom = {
          x: sameTargetPoint.x / viewport.width,
          y: sameTargetPoint.y / viewport.height,
          level: 1.8,
        };
      }
    }
    report.push({ zoom });
  }

  return report;
}

async function captionRenderDataForTrace(sourceDir, captions) {
  if (captions.length === 0) {
    return { inputZoomReport: [], captionsSrtPath: null };
  }
  const require = createRequire(import.meta.url);
  const recastDist = path.dirname(require.resolve('playwright-recast'));
  const parserPath = pathToFileURL(path.join(recastDist, 'parse/trace-parser.js')).href;
  const { parseTrace } = await import(parserPath);
  const trace = await parseTrace(`${sourceDir}/trace.zip`);
  try {
    const recordingPageId = trace.frames.at(-1)?.pageId;
    const recordingActions = recordingPageId
      ? trace.actions.filter((action) => action.pageId === recordingPageId)
      : trace.actions;
    const entries = buildCaptionSrtEntries(
      recordingActions,
      captions,
      recordingActions[0]?.startTime ?? 0,
    );
    const expectedCount = captions.filter(Boolean).length;
    if (entries.length !== expectedCount) {
      throw new Error(`Could not create all caption dwells (${entries.length}/${expectedCount})`);
    }
    const captionsSrtPath = path.join(sourceDir, 'captions.source.srt');
    await fs.writeFile(captionsSrtPath, writeCaptionSrt(entries));
    return {
      inputZoomReport: buildInputZoomReport(trace.actions, trace.metadata.viewport, captions),
      captionsSrtPath,
    };
  } finally {
    trace.frameReader.dispose();
  }
}
/**
 * Keep cursor cues at meaningful interaction boundaries. Rendering every
 * pointer sample from a drag creates an impractically large FFmpeg expression;
 * the recorded video itself retains the smooth drag motion.
 */
export function shouldRenderCursorAction(action) {
  return ['click', 'mouseDown', 'mouseUp'].includes(action?.method);
}

// Trim idle time and speed through navigation/network waits while keeping user
// actions at real time. `cutDelays` pushes the dead stretches faster still.
export function speedConfig({ cutDelays = false } = {}) {
  // autoZoom's zoompan fps scales with the speed multiplier and overflows the
  // encoder above ~6x (producing a zero-duration file), so the transit
  // fast-forward stays conservatively below that while still turning a ~15s build
  // spinner into a ~3s beat.
  const transitSpeed = cutDelays ? 5.0 : 4.0;
  return {
    duringUserAction: 1.0,
    duringNavigation: 2.0,
    duringNetworkWait: 10.0,
    duringIdle: cutDelays ? 6.0 : 3.0,
    // The built-in classifier keys off trace actions + network, which mislabels a
    // long async turn (a "generating…" spinner is a bare waitForFunction with a
    // live request behind it) as user-action/network-wait and leaves it near
    // real-time. Key off the user-action timeline instead: any span with no user
    // action within GAP_HOLD_MS on either side is dead time and collapses to a
    // beat, so the demo dwells on interactions rather than spinners.
    rules: [
      // Caption text is mapped to the replay runner's dwell before each narrated
      // action. Keeping these waits at real time guarantees a readable 3-second
      // window instead of speeding it away with the surrounding idle time.
      {
        name: 'keep-caption-dwells',
        match: (ctx) =>
          ctx.activeActions?.some(
            (action) =>
              action.method === 'waitForTimeout' &&
              action.endTime - action.startTime >= MIN_CAPTION_DURATION_MS,
          ) ?? false,
        speed: 1.0,
      },
      {
        name: 'compress-transit-gaps',
        match: (ctx) =>
          ctx.timeSinceLastAction > GAP_HOLD_MS && ctx.timeUntilNextAction > GAP_HOLD_MS,
        speed: transitSpeed,
      },
    ],
    // A compressed build finishes streaming its result just as the fast-forward
    // ends; hold subtitles briefly so the next caption doesn't land mid-collapse.
    postFastForwardSettleMs: 400,
  };
}

/**
 * Build the recast pipeline for a recorded source directory. The `Recast`
 * builder is injected so the stage chain can be asserted in tests without ffmpeg.
 */
export function buildRenderPipeline(Recast, sourceDir, options = {}) {
  const {
    format = 'mp4',
    resolution = '1080p',
    cutDelays = false,
    captions = [],
    captionsSrtPath = null,
    inputZoomReport = [],
    burnSubtitles,
  } = options;
  const hasCaptions = captions.length > 0;

  let pipeline = Recast.from(sourceDir).parse().speedUp(speedConfig({ cutDelays }));

  // autoZoom binds to subtitle time-windows, so a subtitle stage must precede it.
  // With narration captions (features 6/8) we map them onto the trace actions and
  // burn them in; otherwise we derive windows from the trace only to drive zoom
  // and keep them off-screen.
  if (hasCaptions) {
    pipeline = captionsSrtPath
      ? pipeline.subtitlesFromSrt(captionsSrtPath)
      : pipeline.subtitles(createCaptionTextFn(captions));
  } else {
    pipeline = pipeline.subtitlesFromTrace();
  }

  if (inputZoomReport.length > 0 && !captionsSrtPath) {
    pipeline = pipeline.enrichZoomFromReport(inputZoomReport);
  }

  if (!captionsSrtPath) {
    pipeline = pipeline
      // feature 3: zoom to click/fill targets. Push the levels past the defaults
      // (1.5/1.6) so each action reads clearly, and bias toward the viewport centre
      // so the punch-in doesn't fling the target to a corner.
      // Keep zoom reserved for the actual text entry. Click zooms compete with
      // nearby typing windows in dense traces and can shift the focus away from
      // the field being edited.
      .autoZoom({ clickLevel: 1.0, inputLevel: 1.8, centerBias: 0.35, transitionMs: 450 });
  }

  pipeline = pipeline
    .cursorOverlay({
      filter: shouldRenderCursorAction,
      moveDurationMs: 400,
      hideAfterMs: 1000,
    }) // features 1-2: synthesized gliding cursor
    .clickEffect(); // click ripples

  return pipeline.render({
    format,
    resolution,
    burnSubtitles: burnSubtitles ?? hasCaptions,
  });
}

/** Render a recorded trace/video directory into a polished demo video. */
export async function renderVideo(options = {}) {
  const { sourceDir, outPath, Recast = DefaultRecast, ...pipelineOptions } = options;

  if (!sourceDir) {
    throw new Error('renderVideo requires a sourceDir');
  }
  if (!outPath) {
    throw new Error('renderVideo requires an outPath');
  }

  const captionRenderData = await captionRenderDataForTrace(
    sourceDir,
    pipelineOptions.captions ?? [],
  );
  await buildRenderPipeline(Recast, sourceDir, { ...pipelineOptions, ...captionRenderData }).toFile(
    outPath,
  );
  return outPath;
}
