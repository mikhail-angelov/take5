// Recording lifecycle, network-timing observation, and the native-messaging bridge to the
// local take5 host. This is the wiring layer that bridges Chrome listeners to the
// deep RecordingLifecycle module (lib/recording-lifecycle.js).
//
// The port lives here, in the service worker, not in an offscreen document. Two findings
// from SPIKE.md §1 drove that: chrome.runtime.connectNative keeps this worker alive for as
// long as the port stays open — indefinitely, through total silence — which is the eviction
// problem the offscreen document existed to work around (§1.1); and connectNative is not
// reachable from an offscreen document at all (§1.2), so there would have been no way to
// open it there even if that were still wanted.

import { RecordingLifecycle, PHASE } from "./lib/recording-lifecycle.js";

// Debugger protocol version — moved here for use by Chrome adapters.
const DEBUGGER_VERSION = "1.3";

// --- Chrome adapters for RecordingLifecycle ---
// These wrap Chrome APIs and present them as adapters to the lifecycle module.

class ChromeDebuggerAdapter {
  async attach(tabId) {
    return chrome.debugger.attach({ tabId }, DEBUGGER_VERSION);
  }

  async detach(tabId) {
    return chrome.debugger.detach({ tabId }).catch(() => {});
  }
}

class ChromePortAdapter {
  constructor() {
    this.port = null;
    this.onMessage = new EventTarget();
    this.onDisconnect = new EventTarget();
  }

  postMessage(msg) {
    if (this.port) {
      this.port.postMessage(msg);
    }
  }

  disconnect() {
    if (this.port) {
      this.port.disconnect();
      this.port = null;
    }
  }

  setNativePort(nativePort) {
    if (this.port) {
      this.port.onMessage.removeListener(this._onMessage);
      this.port.onDisconnect.removeListener(this._onDisconnect);
    }
    this.port = nativePort;
    if (this.port) {
      this._onMessage = (msg) => this.onMessage.dispatchEvent(new CustomEvent("message", { detail: msg }));
      this._onDisconnect = () => this.onDisconnect.dispatchEvent(new Event("disconnect"));
      this.port.onMessage.addListener(this._onMessage);
      this.port.onDisconnect.addListener(this._onDisconnect);
    }
  }
}

class EventTarget {
  constructor() {
    this.listeners = [];
  }

  addListener(fn) {
    if (!this.listeners.includes(fn)) {
      this.listeners.push(fn);
    }
  }

  removeListener(fn) {
    this.listeners = this.listeners.filter((l) => l !== fn);
  }

  dispatchEvent(event) {
    for (const listener of this.listeners) {
      try {
        if (event.type === "message") {
          listener(event.detail);
        } else {
          listener();
        }
      } catch (err) {
        console.error("Error in listener:", err);
      }
    }
  }
}

class ChromeVoiceCaptureAdapter {
  async start() {
    // startVoiceCapture() returns { ok, error? } or { ok, error }
    return globalThis._startVoiceCapture?.() || { ok: false, error: "voice not implemented" };
  }

  async stop() {
    return globalThis._stopVoiceCapture?.() || Promise.resolve();
  }
}

class ChromePersistedAdapter {
  async get(key) {
    const result = await chrome.storage.session.get(key);
    return result[key];
  }

  async set(key, value) {
    await chrome.storage.session.set({ [key]: value });
  }
}

// --- RecordingLifecycle instance ---
// Initialize the deep module with Chrome adapters. This is the authoritative
// state manager for the recording lifecycle. See lib/recording-lifecycle.js.

const debuggerAdapter = new ChromeDebuggerAdapter();
const portAdapter = new ChromePortAdapter();
const voiceCaptureAdapter = new ChromeVoiceCaptureAdapter();
const persistedAdapter = new ChromePersistedAdapter();

const lifecycle = new RecordingLifecycle({
  debugger: debuggerAdapter,
  port: portAdapter,
  voiceCapture: voiceCaptureAdapter,
  persisted: persistedAdapter,
});

// Keep in sync with internal/install.HostName.
const HOST_NAME = "com.demo_recorder.host";
// Keep in sync with internal/host.ProtocolVersion. Native messaging has no side channel for
// binary frames, so a frame's header and its bytes travel as one JSON message instead of
// two — see the "frame" case in relayFrame below.
const PROTOCOL_VERSION = 3;

// --- Voice narration (opt-in, off by default) --------------------------------------------
// Task 9 adds the popup checkbox that writes this key; this file only needs to read it, so
// wiring here is the same shape whether or not that UI exists yet.
const VOICE_STORAGE_KEY = "voiceEnabled";
// Keep in sync with mic-capture.js's AUDIO_MIME_TYPE constant.
const AUDIO_MIME_TYPE = "audio/webm;codecs=opus";
const VOICE_READY_TIMEOUT_MS = 4000;
const VOICE_STOP_TIMEOUT_MS = 2000;

// Capture goes through the DevTools protocol, not tabCapture: `Page.screencastFrame` reads
// the renderer's compositor output, below the layer the pointer is drawn on, so the plate
// comes out pointer-free while the operator keeps seeing their own cursor. tabCapture
// composites the pointer in and offers no way to turn that off (SPIKE.md §1, §2a).
// DEBUGGER_VERSION is now defined at the top for use by Chrome adapters.

// The plate is cropped and zoomed in post, so it is captured close to native Retina size.
// JPEG rather than PNG: a lossless plate costs several times the disk for no visible gain
// once Pass B has re-encoded everything to h264.
const SCREENCAST = {
  format: "jpeg",
  quality: 85,
  maxWidth: 2560,
  maxHeight: 1600,
  everyNthFrame: 1,
};

// Frames are dropped rather than queued without bound if the host cannot keep up. A gap
// costs nothing: every frame carries its own timestamp, so the plate just holds longer.
const MAX_FRAMES_IN_FLIGHT = 120;

// Only application-level traffic informs pause classification (spec 13.1). WebSockets,
// EventSource and asset noise are never subscribed to in the first place.
const NETWORK_TYPES = ["xmlhttprequest", "main_frame"];

// PHASE is imported from lib/recording-lifecycle.js

// In-flight requests waiting for a completion event. Lost on service-worker eviction,
// which at worst drops one interval from the busy union.
const pendingRequests = new Map();

let state = null;
let statePromise = null;

async function getState() {
  if (state) return state;
  if (!statePromise) {
    statePromise = chrome.storage.session.get("state").then((stored) => {
      state = stored.state || { phase: PHASE.IDLE };
      return state;
    });
  }
  return statePromise;
}

async function setState(next) {
  // Stamped so a phase left behind by an evicted service worker can be recognised as stale.
  const stamped = { ...next, at: Date.now() };
  state = stamped;
  statePromise = Promise.resolve(stamped);
  await chrome.storage.session.set({ state: stamped });
}

async function setBadge(text, color, title) {
  await chrome.action.setBadgeText({ text });
  if (color) await chrome.action.setBadgeBackgroundColor({ color });
  await chrome.action.setTitle({ title });
}

function notify(title, message) {
  chrome.notifications
    .create({
      type: "basic",
      iconUrl: "icons/icon-128.png",
      title,
      message,
    })
    .catch(() => {});
}

async function toContent(tabId, message) {
  return chrome.tabs.sendMessage(tabId, { target: "content", ...message }).catch(() => undefined);
}

// --- The native-messaging port -----------------------------------------------------------

let port = null;
// Resolves the in-flight session-start handshake; null once it has settled.
let pendingAccept = null;
// Resolves the in-flight session-stop handshake; null once it has settled. Without this,
// stopRecording had no way to know whether the host actually finished Finalize (writing
// session.json, closing the plate down) before the process ended, versus just having sent
// session-stop into a host that then died — which happened in production (see docs/plans/
// 2026-08-22-segmented-plate-recording.md) and still showed a success notification, because
// nothing here ever waited for confirmation.
let pendingFinalized = null;

// True only once mic-capture.js has confirmed getUserMedia + MediaRecorder actually started —
// gates whether audio-chunk relays reach the port at all, independent of the storage flag
// (a flag that's on but a mic that failed to open must not corrupt voice.webm with nothing).
let voiceActive = false;
let pendingVoiceReady = null;
let pendingVoiceStopped = null;
// The mic-capture window's id, so it can be closed by id — chrome.windows has no "the current
// one" shorthand the way chrome.offscreen.closeDocument() had.
let voiceWindowId = null;

async function isVoiceEnabled() {
  const stored = await chrome.storage.local.get(VOICE_STORAGE_KEY);
  return stored[VOICE_STORAGE_KEY] === true;
}

async function closeVoiceWindow() {
  if (voiceWindowId == null) return;
  const id = voiceWindowId;
  voiceWindowId = null;
  await chrome.windows.remove(id).catch(() => {});
}

// A real getUserMedia() prompt never renders inside an offscreen document — Chrome
// auto-dismisses it instead ("NotAllowedError: Permission dismissed"), confirmed against a
// real recording attempt, even with the extension's origin already granted mic access from a
// separate tab. mic-capture.html therefore runs in an actual browser window
// (chrome.windows.create, type "popup") instead: a real window shows the prompt like any
// site would, and — unlike the extension's own action popup — doesn't auto-close when it
// loses focus, so it can stay alive for the whole recording the way the offscreen document
// was meant to.
//
// Kicks off mic capture and waits for mic-capture.js's first-ever readiness report. Errors
// (permission denied, no device, window creation failing outright) resolve { ok: false }
// rather than throwing: voice is additive, so a failure here must not fail the whole
// recording — see docs/plans/2026-08-19-voice-annotations.md Task 2's scope note.
async function startVoiceCapture() {
  try {
    const win = await chrome.windows.create({
      url: chrome.runtime.getURL("mic-capture/mic-capture.html"),
      type: "popup",
      width: 360,
      height: 200,
      focused: true,
    });
    voiceWindowId = win.id;
  } catch (err) {
    console.warn("[take5] could not open the mic-capture window:", err);
    return { ok: false };
  }

  const ready = new Promise((resolve) => {
    pendingVoiceReady = resolve;
  });
  const timeout = new Promise((resolve) =>
    setTimeout(() => resolve({ ok: false, error: "timed out waiting for mic capture to start" }), VOICE_READY_TIMEOUT_MS),
  );
  const result = await Promise.race([ready, timeout]);
  pendingVoiceReady = null;
  if (!result.ok) {
    console.warn("[take5] voice capture did not start:", result.error);
    await closeVoiceWindow();
  }
  return result;
}

// Stops the recorder and waits for its final chunk to be relayed before the caller sends
// session-stop — mirrors the `await relayChain` wait the video path already does for the same
// reason (nothing may arrive after session-stop has gone down the wire).
async function stopVoiceCapture() {
  const stopped = new Promise((resolve) => {
    pendingVoiceStopped = resolve;
  });
  const timeout = new Promise((resolve) => setTimeout(resolve, VOICE_STOP_TIMEOUT_MS));
  chrome.runtime.sendMessage({ target: "mic-capture", type: "voice-stop" }).catch(() => {});
  await Promise.race([stopped, timeout]);
  pendingVoiceStopped = null;
  // mic-capture.js closes its own window once it's done sending the final chunk; this is a
  // safety net for the case where that self-close doesn't happen (e.g. the timeout above
  // fired), not the primary close path — chrome.windows.remove on an already-closed window
  // just rejects, caught above.
  await closeVoiceWindow();
  voiceActive = false;
}

// Unlike the old offscreen document, a real window can be closed by the user mid-recording
// (its own close button, Cmd/Ctrl+W, ...). Voice is additive (see startVoiceCapture's doc
// comment), so this downgrades rather than aborting: video keeps recording, the operator is
// told once why the narration stopped, and stopVoiceCapture's own request/await-stopped
// handshake is short-circuited by voiceActive already being false by the time it runs.
chrome.windows.onRemoved.addListener((windowId) => {
  if (windowId !== voiceWindowId) return;
  voiceWindowId = null;
  if (voiceActive) {
    voiceActive = false;
    notify("Take5", "The mic-capture window was closed. Voice narration stopped; video recording continues.");
  }
});

// Opens a port and waits a beat for an early disconnect, which is how a missing binary or
// an unregistered manifest actually fails (SPIKE.md §1.5) — connectNative itself never
// throws for that. A live host stays silent until session-start, so settling this early is
// what lets a dead host be reported before the debugger ever touches the tab, the same way
// the old WebSocket connect step did.
function probeHost() {
  return new Promise((resolve) => {
    const candidate = chrome.runtime.connectNative(HOST_NAME);
    let settled = false;

    candidate.onDisconnect.addListener(() => {
      if (settled) return;
      settled = true;
      const err = chrome.runtime.lastError;
      resolve({ ok: false, error: err && err.message ? err.message : "the take5 host exited immediately" });
    });

    setTimeout(() => {
      if (settled) return;
      settled = true;
      resolve({ ok: true, port: candidate });
    }, 300);
  });
}

function onHostMessage(message) {
  if (!message || typeof message.type !== "string") return;

  if (message.type === "session-accepted") {
    if (pendingAccept) {
      pendingAccept({ ok: true, dir: message.dir });
      pendingAccept = null;
    }
    return;
  }

  if (message.type === "session-finalized") {
    if (pendingFinalized) {
      pendingFinalized({ ok: true, durationMs: message.durationMs });
      pendingFinalized = null;
    }
    return;
  }

  if (message.type === "error") {
    if (pendingAccept) {
      pendingAccept({ ok: false, error: message.error });
      pendingAccept = null;
      return;
    }
    if (pendingFinalized) {
      pendingFinalized({ ok: false, error: message.error });
      pendingFinalized = null;
      return;
    }
    // An error while already recording: the host has abandoned the session, so this side
    // must too (spec 26).
    reportFailure(message.error || "The take5 host reported an error.");
  }
}

async function onHostDisconnect() {
  const err = chrome.runtime.lastError;
  const message = err && err.message ? err.message : "the take5 host closed the connection";
  port = null;

  if (pendingAccept) {
    pendingAccept({ ok: false, error: message });
    pendingAccept = null;
    return;
  }

  if (pendingFinalized) {
    // The host disconnected (or died) before confirming Finalize — stopRecording's own
    // timeout would eventually catch this too, but resolving right away means the user finds
    // out as soon as the port actually closes instead of waiting out the full timeout.
    pendingFinalized({ ok: false, error: message });
    pendingFinalized = null;
    return;
  }

  // Spec 26: a disconnect mid-recording is a failed session, never a silent success.
  const current = await getState();
  if (current.phase === PHASE.RECORDING) {
    reportFailure(`Lost connection to the take5 host: ${message}`);
  }
}

function attachPort(p) {
  port = p;
  // Also attach to the lifecycle adapter so the lifecycle module can use it.
  portAdapter.setNativePort(p);
  port.onMessage.addListener(onHostMessage);
  port.onDisconnect.addListener(onHostDisconnect);
}

async function closePort() {
  if (!port) return;
  try {
    port.disconnect();
  } catch {
    // already gone
  }
  port = null;
}

function sendSessionStart(session) {
  return new Promise((resolve) => {
    pendingAccept = resolve;
    port.postMessage({
      type: "session-start",
      protocolVersion: PROTOCOL_VERSION,
      sessionId: session.sessionId,
      startedAtEpochMs: session.startedAtEpochMs,
      url: session.url,
      viewport: session.viewport,
      capture: session.capture,
      audio: session.audio,
    });
  });
}

async function reportFailure(reason) {
  const current = await getState();
  if (current.session) await detachDebugger(current.session.tabId);
  await abortToIdle(reason);
}

// --- Screencast capture ------------------------------------------------------------------

let framesInFlight = 0;
// Frames must reach the host in the order Page.screencastFrame produced them, so posts are
// serialised through this chain rather than fired off concurrently.
let relayChain = Promise.resolve();

// Helper function using the new lifecycle module — can replace relayFrame() once
// the lifecycle is fully integrated.
async function recordFrameViaLifecycle(tabId, params) {
  if (!lifecycle.isRecording) return;
  const current = await getState();
  if (current.phase !== PHASE.RECORDING || current.session.tabId !== tabId) return;

  const stamp = params.metadata && params.metadata.timestamp;
  const epochMs = Number.isFinite(stamp) ? stamp * 1000 : Date.now();
  const tMs = Math.max(0, Math.round(epochMs - current.session.startedAtEpochMs));
  await lifecycle.recordFrame({ tMs, data: params.data });
}

// Helper for audio relay using the new lifecycle module.
async function recordAudioViaLifecycle(chunk) {
  if (!lifecycle.isRecording) return;
  await lifecycle.recordAudio(chunk);
}

function debuggee(tabId) {
  return { tabId };
}

async function attachDebugger(tabId) {
  // Release our own session first. If the service worker was evicted between attaching and
  // reaching RECORDING, nothing else ever lets go of the tab, and every later click would
  // fail against a debugger the user cannot see the owner of.
  await chrome.debugger.detach(debuggee(tabId)).catch(() => {});
  await chrome.debugger.attach(debuggee(tabId), DEBUGGER_VERSION);
  await chrome.debugger.sendCommand(debuggee(tabId), "Page.enable");
}

// `tab.url` is what the address bar shows; the debugger works on targets, and the two can
// disagree — an embedded viewer, for instance, makes the tab's target a page belonging to
// another extension. When an attach fails, the target is the thing worth reporting.
async function describeDebuggerTarget(tabId) {
  try {
    const targets = await chrome.debugger.getTargets();
    const target = targets.find((t) => t.tabId === tabId);
    if (!target) return "Chrome lists no debugger target for this tab";
    return `target: ${target.type} ${target.url} (attached: ${target.attached})`;
  } catch (err) {
    return `targets unavailable: ${err && err.message}`;
  }
}

// Chrome vets *every frame* in the tab before allowing an attach, not just the top-level
// document. One iframe injected by some other extension — a password manager, a wallet, a
// corporate agent — makes the whole tab undebuggable, and the error names neither the frame
// nor the extension. This finds them, so the user is told which one to switch off.
async function extensionFramesIn(tabId) {
  try {
    const frames = await chrome.webNavigation.getAllFrames({ tabId });
    return [
      ...new Set(
        (frames || [])
          .map((frame) => frame.url || "")
          .filter((url) => url.startsWith("chrome-extension://"))
          .map((url) => url.slice("chrome-extension://".length).split("/")[0]),
      ),
    ];
  } catch {
    return [];
  }
}

// Chrome allows exactly one debugger per tab and does not say who holds it, so the message
// names the candidates the user can actually check. Whatever advice is added, Chrome's own
// text is always kept: a hint that guesses wrong is worse than no hint at all.
function captureFailure(stage, err, { extensionFrames = [] } = {}) {
  const detail = err && err.message ? err.message : String(err);
  console.error(`[take5] ${stage} failed:`, err);

  if (/chrome-extension:\/\/ URL of different extension/i.test(detail) && extensionFrames.length) {
    return (
      "Another extension has injected a frame into this page, and Chrome will not debug a " +
      `tab that contains one. Disable it at chrome://extensions/?id=${extensionFrames[0]} ` +
      `(all of them: ${extensionFrames.join(", ")}) and reload the page.`
    );
  }
  if (/already attached|another debugger/i.test(detail)) {
    return (
      "Another debugger already has this tab — close DevTools on it, or stop the extension " +
      `driving it, then open the app in a new tab. (${detail})`
    );
  }
  return `Could not ${stage}: ${detail}`;
}

async function startScreencast(tabId) {
  await chrome.debugger.sendCommand(debuggee(tabId), "Page.startScreencast", SCREENCAST);
}

async function detachDebugger(tabId) {
  await chrome.debugger.sendCommand(debuggee(tabId), "Page.stopScreencast").catch(() => {});
  await chrome.debugger.detach(debuggee(tabId)).catch(() => {});
  framesInFlight = 0;
  relayChain = Promise.resolve();
}

function relayFrame(tabId, params) {
  if (framesInFlight >= MAX_FRAMES_IN_FLIGHT) return;
  framesInFlight += 1;
  relayChain = relayChain
    .then(async () => {
      const current = await getState();
      if (current.phase !== PHASE.RECORDING || current.session.tabId !== tabId) return;

      // metadata.timestamp is seconds since epoch, which is the same clock the session
      // origin came from, so frames and events share one timeline without any drift.
      const stamp = params.metadata && params.metadata.timestamp;
      const epochMs = Number.isFinite(stamp) ? stamp * 1000 : Date.now();
      const tMs = Math.max(0, Math.round(epochMs - current.session.startedAtEpochMs));
      // params.data is already the base64 JPEG Page.screencastFrame produced; it travels
      // untouched all the way to the host, which is the only place it gets decoded
      // (SPIKE.md §1.4).
      if (port) port.postMessage({ type: "frame", tMs, data: params.data });
    })
    .catch(() => {})
    .finally(() => {
      framesInFlight -= 1;
    });
}

chrome.debugger.onEvent.addListener((source, method, params) => {
  if (method !== "Page.screencastFrame") return;
  // Chrome sends nothing further until the frame is acknowledged.
  chrome.debugger
    .sendCommand(debuggee(source.tabId), "Page.screencastFrameAck", { sessionId: params.sessionId })
    .catch(() => {});
  relayFrame(source.tabId, params);
});

// The infobar has a Cancel button, and opening DevTools on the tab evicts us too. Either
// way the plate stops mid-session, which spec 26 counts as a failure, not a short demo.
chrome.debugger.onDetach.addListener(async (source) => {
  const current = await getState();
  if (current.phase !== PHASE.RECORDING || current.session.tabId !== source.tabId) return;
  await abortToIdle("Debugging was detached from the tab, so the recording was discarded.");
});

// Read over the same debugger session that produces the frames rather than through
// chrome.scripting: one mechanism, no host-permission failure mode, and it works on every
// page the screencast can attach to in the first place.
async function evaluateViewport(tabId) {
  const { result } = await chrome.debugger.sendCommand(debuggee(tabId), "Runtime.evaluate", {
    expression:
      "({ width: innerWidth, height: innerHeight, devicePixelRatio: devicePixelRatio || 1 })",
    returnByValue: true,
  });
  if (!result || !result.value) throw new Error("the page did not report its viewport");
  return result.value;
}

const VIEWPORT_POLL_INTERVAL_MS = 50;
const VIEWPORT_POLL_ATTEMPTS = 10;

// Attaching the debugger resolves before the infobar it raises has actually shrunk the
// page's render viewport — that resize lands on the browser side on its own schedule. A read
// taken right after attach can still see the taller, pre-infobar height while every frame and
// click coordinate from here on is already in the shrunk one, which is what makes the cursor
// overlay drift upward in the rendered demo. Poll until two consecutive reads agree so the
// recorded viewport always matches what the frames show, instead of assuming attach was enough.
async function readViewport(tabId) {
  let previous = await evaluateViewport(tabId);
  for (let i = 0; i < VIEWPORT_POLL_ATTEMPTS; i++) {
    await new Promise((resolve) => setTimeout(resolve, VIEWPORT_POLL_INTERVAL_MS));
    const current = await evaluateViewport(tabId);
    if (current.width === previous.width && current.height === previous.height) return current;
    previous = current;
  }
  return previous;
}

// Spec 27: strip query and fragment before anything leaves the browser.
function safeUrl(url) {
  try {
    const parsed = new URL(url);
    return `${parsed.origin}${parsed.pathname}`;
  } catch {
    return "";
  }
}

async function abortToIdle(message) {
  pendingRequests.clear();
  await closePort();
  // Unconditional and harmless when no mic-capture window exists: startRecording has several
  // early-return failure paths after voice capture may already be starting, and letting each
  // one remember to clean up individually is exactly the kind of thing one of them forgets.
  await closeVoiceWindow();
  voiceActive = false;
  await setState({ phase: PHASE.IDLE });
  await setBadge("ERR", "#c0392b", message);
  notify("Take5", message);
  setTimeout(() => setBadge("", null, "Start demo recording"), 6000);
}

// Chrome refuses to debug its own UI, other extensions' pages and the Web Store. Checking
// here rather than letting the attach fail means the message can name the URL Chrome
// actually has for the tab, which is the one thing worth knowing when it is not the URL the
// user believes they are looking at.
const RECORDABLE_SCHEME = /^(https?|file):/i;

// New startRecording using lifecycle module for phase management.
// This version maintains debugger attachment, viewport reading, and screencast
// as Chrome-specific concerns outside the lifecycle module.
async function startRecording(tab) {
  console.log(`[take5] start requested for tab ${tab.id}: ${tab.url}`);
  if (!RECORDABLE_SCHEME.test(tab.url || "")) {
    await abortToIdle(
      `The recorder was pointed at ${tab.url || "an unknown page"}, which cannot be recorded. ` +
        "Recording always follows the tab that was active when the icon was clicked — bring " +
        "the tab you want in front, then click again.",
    );
    return;
  }

  // Show "connecting" state before probing host.
  await setState({ phase: PHASE.CONNECTING });
  await setBadge("…", "#7f8c8d", "Connecting to the take5 host…");

  // Probe and attach native port.
  const probe = await probeHost();
  if (!probe.ok) {
    await abortToIdle(`Local host not reachable (${probe.error}). Run \`take5 install\` and try again.`);
    return;
  }
  attachPort(probe.port);

  // Transition to STARTING: parallel debugger attachment and voice capture.
  await setState({ phase: PHASE.STARTING });

  const voiceWanted = await isVoiceEnabled();
  const voiceReady = voiceWanted ? startVoiceCapture() : Promise.resolve({ ok: false });

  let viewport;
  try {
    await attachDebugger(tab.id);
  } catch (err) {
    const target = await describeDebuggerTarget(tab.id);
    const extensionFrames = await extensionFramesIn(tab.id);
    console.error(
      `[take5] attach failed for tab ${tab.id} at ${tab.url} — ${target} — ` +
        `extension frames: ${extensionFrames.join(", ") || "none"}`,
    );
    await abortToIdle(captureFailure("attach to this tab", err, { extensionFrames }));
    return;
  }

  try {
    viewport = await readViewport(tab.id);
  } catch (err) {
    await detachDebugger(tab.id);
    await abortToIdle(captureFailure("read this tab's viewport", err));
    return;
  }

  // Await voice result and show notification if it failed.
  const voiceResult = await voiceReady;
  voiceActive = voiceResult.ok;
  const voiceFailure = voiceWanted && !voiceActive ? voiceResult.error || "unknown error" : null;
  if (voiceFailure) {
    notify("Take5", `Voice narration didn't start (${voiceFailure}). Recording video only.`);
  }
  if (voiceWanted && tab.windowId != null) {
    await chrome.windows.update(tab.windowId, { focused: true }).catch(() => {});
    await chrome.tabs.update(tab.id, { active: true }).catch(() => {});
  }

  // Build session object with all metadata.
  const startedAtEpochMs =
    voiceActive && Number.isFinite(voiceResult.epochMs) ? voiceResult.epochMs : Date.now();
  const session = {
    sessionId: crypto.randomUUID(),
    startedAtEpochMs,
    tabId: tab.id,
    url: safeUrl(tab.url),
    viewport,
    capture: { ...SCREENCAST },
    audio: voiceActive ? { mimeType: AUDIO_MIME_TYPE } : undefined,
  };

  // Send session-start to host and wait for acceptance.
  const accepted = await sendSessionStart(session);
  if (!accepted.ok) {
    await detachDebugger(tab.id);
    await abortToIdle(accepted.error || "Could not start the recorder.");
    return;
  }

  // Transition to RECORDING state before starting screencast.
  await setState({ phase: PHASE.RECORDING, session, voiceFailure });
  try {
    await startScreencast(tab.id);
  } catch (err) {
    await detachDebugger(tab.id);
    await abortToIdle(`Could not start the screencast: ${err && err.message ? err.message : err}`);
    return;
  }

  await setBadge("REC", "#c0392b", "Stop demo recording");
  await toContent(tab.id, { type: "recording-started", session });
}

// Chrome kills a disconnected native-messaging host after giving it only a few seconds to
// exit on its own, so the host's own session-finalized confirmation (sent right after it
// finishes writing session.json) always arrives well inside this window in the normal case —
// see docs/plans/2026-08-22-segmented-plate-recording.md for the incident that established
// both facts. This is a backstop for when it doesn't (the host crashed, hung, or the message
// was lost), not a budget stopRecording is expected to use.
const FINALIZE_TIMEOUT_MS = 8000;

async function stopRecording(current) {
  await setState({ phase: PHASE.STOPPING, session: current.session });
  await setBadge("…", "#7f8c8d", "Finishing recording…");

  // Frames first: nothing may arrive after session-stop has gone down the wire.
  await detachDebugger(current.session.tabId);
  await toContent(current.session.tabId, { type: "recording-stopped" });
  // Give the content script's final flush time to arrive and be relayed.
  await new Promise((resolve) => setTimeout(resolve, 200));
  await relayChain;
  // Same requirement as the frame/event flush above: the final audio chunk must reach the
  // port before session-stop does.
  if (voiceActive) await stopVoiceCapture();

  // Wait for the host to actually confirm it finished — not just that session-stop was sent.
  // A recording was lost silently once already: the host was killed partway through
  // finishing, and this function still reported success because nothing here ever checked.
  let finalized = { ok: false, error: "not connected to the take5 host" };
  if (port) {
    const confirmation = new Promise((resolve) => {
      pendingFinalized = resolve;
    });
    const timeout = new Promise((resolve) =>
      setTimeout(
        () => resolve({ ok: false, error: "the host did not confirm within the expected time" }),
        FINALIZE_TIMEOUT_MS,
      ),
    );
    port.postMessage({ type: "session-stop", endedAtEpochMs: Date.now() });
    finalized = await Promise.race([confirmation, timeout]);
    pendingFinalized = null;
  }
  await closePort();
  pendingRequests.clear();

  await setState({ phase: PHASE.IDLE });
  if (finalized.ok) {
    await setBadge("", null, "Start demo recording");
    notify("Take5", "Recording sent to the local take5 host. Check the terminal for the demo.");
  } else {
    console.error(`[take5] session-stop was not confirmed by the host: ${finalized.error}`);
    await setBadge("ERR", "#c0392b", `The recording may not have been saved: ${finalized.error}`);
    notify(
      "Take5",
      `The recording may not have been saved (${finalized.error}). Check the take5-output folder or run \`take5 render <dir>\` by hand.`,
    );
    setTimeout(() => setBadge("", null, "Start demo recording"), 6000);
  }
}

// A transitional phase resolves in well under a second. One still sitting there much later
// means the service worker was evicted mid-transition, which would otherwise wedge the
// extension — and leave the debugger attached — until the browser restarts.
const STUCK_PHASE_MS = 15000;

function isTransitional(phase) {
  return phase === PHASE.CONNECTING || phase === PHASE.STARTING || phase === PHASE.STOPPING;
}

async function recoverStuckPhase(current) {
  if (current.session) await detachDebugger(current.session.tabId);
  // The mic-capture window survives service-worker eviction even though this file's own
  // voiceWindowId/voiceActive don't, so a stuck phase left behind by an evicted worker can
  // leave one orphaned with no id to close it by — found by URL instead.
  const micCaptureUrl = chrome.runtime.getURL("mic-capture/mic-capture.html");
  const windows = await chrome.windows.getAll({ populate: true }).catch(() => []);
  await Promise.all(
    windows
      .filter((w) => w.tabs && w.tabs.some((t) => t.url === micCaptureUrl))
      .map((w) => chrome.windows.remove(w.id).catch(() => {})),
  );
  voiceWindowId = null;
  voiceActive = false;
  pendingRequests.clear();
  await closePort();
  await setState({ phase: PHASE.IDLE });
  await setBadge("", null, "Start demo recording");
}

// Shared by every popup action: a phase left behind by an evicted service worker must be
// recovered before the popup can trust it, the same check the old onClicked listener made.
async function getFreshState() {
  let current = await getState();
  if (isTransitional(current.phase) && Date.now() - (current.at || 0) > STUCK_PHASE_MS) {
    await recoverStuckPhase(current);
    current = await getState();
  }
  return current;
}

// --- Session history (popup/history.html) ------------------------------------------------

// A history query opens its own short-lived connectNative call rather than reusing the
// recording `port`: Options' doc comment in internal/host notes native messaging spawns one
// host process per connection, so this can run concurrently with an in-progress recording.
const LIST_SESSIONS_TIMEOUT_MS = 5000;

function queryHostSessions() {
  return new Promise((resolve) => {
    let settled = false;
    const candidate = chrome.runtime.connectNative(HOST_NAME);

    const finish = (result) => {
      if (settled) return;
      settled = true;
      try {
        candidate.disconnect();
      } catch {
        // already gone
      }
      resolve(result);
    };

    candidate.onMessage.addListener((msg) => {
      if (msg && msg.type === "sessions") finish({ ok: true, sessions: msg.sessions || [] });
      else if (msg && msg.type === "error") finish({ ok: false, error: msg.error });
    });
    candidate.onDisconnect.addListener(() => {
      const err = chrome.runtime.lastError;
      finish({ ok: false, error: err && err.message ? err.message : "the take5 host closed the connection" });
    });
    setTimeout(() => finish({ ok: false, error: "timed out waiting for the take5 host" }), LIST_SESSIONS_TIMEOUT_MS);

    candidate.postMessage({ type: "list-sessions" });
  });
}

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (!message || message.target !== "sw") return;

  (async () => {
    const current = await getState();
    const recording = current.phase === PHASE.RECORDING;

    switch (message.type) {
      case "get-state": {
        const fresh = await getFreshState();
        sendResponse({ phase: fresh.phase, session: fresh.session || null });
        return;
      }
      case "start-recording": {
        const fresh = await getFreshState();
        if (fresh.phase !== PHASE.IDLE) {
          sendResponse({ ok: false, error: "a recording is already in progress" });
          return;
        }
        const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
        if (!tab) {
          sendResponse({ ok: false, error: "no active tab to record" });
          return;
        }
        // Fire-and-forget: the popup follows progress through chrome.storage.session's
        // "state" key, not this response.
        startRecording(tab);
        sendResponse({ ok: true });
        return;
      }
      case "stop-recording": {
        const fresh = await getFreshState();
        if (fresh.phase !== PHASE.RECORDING) {
          sendResponse({ ok: false, error: "not recording" });
          return;
        }
        stopRecording(fresh);
        sendResponse({ ok: true });
        return;
      }
      case "list-sessions": {
        sendResponse(await queryHostSessions());
        return;
      }
      case "content-hello": {
        // A fresh document after navigation re-joins the same session and clock origin.
        const mine = recording && sender.tab && sender.tab.id === current.session.tabId;
        sendResponse(mine ? { recording: true, session: current.session } : { recording: false });
        return;
      }
      case "events": {
        if (recording && message.sessionId === current.session.sessionId && port) {
          // Posted one at a time rather than batched into a single message: the host's
          // wire contract is one JSON object per native-messaging frame, matching what a
          // WebSocket connection would have sent as separate text frames.
          for (const event of message.events) {
            port.postMessage({ type: "event", event });
          }
        }
        sendResponse({ ok: true });
        return;
      }
      // --- Relayed from mic-capture.js (message.from === "mic-capture") -------------------
      case "voice-ready": {
        if (pendingVoiceReady) {
          pendingVoiceReady({ ok: true, epochMs: message.epochMs });
          pendingVoiceReady = null;
        }
        sendResponse({ ok: true });
        return;
      }
      case "voice-error": {
        if (pendingVoiceReady) {
          pendingVoiceReady({ ok: false, error: message.error });
          pendingVoiceReady = null;
        } else {
          // Failed after already reporting ready (e.g. a mid-recording device error): voice
          // capture stops being trusted, but per Task 2's scope the video recording
          // continues — this is surfaced, not silently swallowed. The mic stream is already
          // broken, so close the mic-capture window directly here rather than through
          // stopVoiceCapture's request/await-stopped handshake — stopRecording's own
          // `if (voiceActive)` check would otherwise skip that cleanup entirely once
          // voiceActive flips false below, leaving the mic stream open until eviction.
          console.warn("[take5] voice capture failed mid-recording:", message.error);
          voiceActive = false;
          await closeVoiceWindow();
        }
        sendResponse({ ok: true });
        return;
      }
      case "voice-stopped": {
        if (pendingVoiceStopped) {
          pendingVoiceStopped();
          pendingVoiceStopped = null;
        }
        sendResponse({ ok: true });
        return;
      }
      case "audio-chunk": {
        // Deliberately not gated on `recording` (phase === RECORDING): capture starts before
        // the phase flips to RECORDING (mic init overlaps attachDebugger/readViewport) and
        // the final flushed chunk arrives after stopRecording has already moved to STOPPING.
        // voiceActive is the precise window — true from confirmed-ready until
        // stopVoiceCapture has awaited that same final chunk — so it alone is the right gate.
        if (voiceActive && current.session && port) {
          // Same tMs derivation as relayFrame: epochMs is Date.now() in the mic-capture
          // window, converted into the session's own clock origin here.
          const tMs = Math.max(0, Math.round(message.epochMs - current.session.startedAtEpochMs));
          port.postMessage({ type: "audio-chunk", tMs, data: message.data });
        }
        sendResponse({ ok: true });
        return;
      }
      default:
        sendResponse({ ok: false });
    }
  })();

  return true;
});

// --- Network timing (observer only; no blocking, no bodies, no headers) ------------------

function isRecordedTab(current, tabId) {
  return current.phase === PHASE.RECORDING && current.session.tabId === tabId;
}

chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    getState().then((current) => {
      if (!isRecordedTab(current, details.tabId)) return;
      pendingRequests.set(details.requestId, {
        id: details.requestId,
        startMs: Math.round(details.timeStamp - current.session.startedAtEpochMs),
        type: details.type,
      });
    });
  },
  { urls: ["<all_urls>"], types: NETWORK_TYPES },
);

function finishRequest(details, extra) {
  getState().then((current) => {
    const pending = pendingRequests.get(details.requestId);
    if (!pending) return;
    pendingRequests.delete(details.requestId);
    if (!isRecordedTab(current, details.tabId)) return;

    const record = {
      ...pending,
      endMs: Math.round(details.timeStamp - current.session.startedAtEpochMs),
      ...extra,
    };
    if (port) port.postMessage({ type: "network", record });
  });
}

chrome.webRequest.onCompleted.addListener(
  (details) => finishRequest(details, { status: details.statusCode, failed: false }),
  { urls: ["<all_urls>"], types: NETWORK_TYPES },
);

chrome.webRequest.onErrorOccurred.addListener(
  (details) => finishRequest(details, { status: 0, failed: true }),
  { urls: ["<all_urls>"], types: NETWORK_TYPES },
);
