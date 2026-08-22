// Runs in every top-level document. Idle and nearly free unless the tab is being recorded.
//
// It re-runs from scratch after every navigation (spec 6.4), so it always re-asks the
// service worker for the live session and keeps using that session's clock origin.

(() => {
  // Chrome can inject the same document twice (extension reload, prerendered new-tab
  // documents); a second set of listeners would double every event.
  if (globalThis.__demoRecorderContentScript) return;
  globalThis.__demoRecorderContentScript = true;

  const { describeTarget } = globalThis.__demoRecorderTargetMetadata;
  const { createEventCapture } = globalThis.__demoRecorderEventCapture;

  const FLUSH_INTERVAL_MS = 250;

  let session = null;
  let queue = [];
  let flushTimer = null;
  let capture = null;

  function now() {
    return Date.now() - session.startedAtEpochMs;
  }

  function send(message) {
    // The service worker may be asleep or gone; losing telemetry must never break the page.
    return chrome.runtime.sendMessage(message).catch(() => undefined);
  }

  function flush() {
    flushTimer = null;
    if (!queue.length || !session) return;
    const events = queue;
    queue = [];
    send({ target: "sw", type: "events", sessionId: session.sessionId, events });
  }

  function emit(event) {
    queue.push(event);
    if (!flushTimer) flushTimer = setTimeout(flush, FLUSH_INTERVAL_MS);
  }

  function viewport() {
    return {
      width: window.innerWidth,
      height: window.innerHeight,
      devicePixelRatio: window.devicePixelRatio || 1,
    };
  }

  function startCapturing(activeSession) {
    if (capture) return;
    session = activeSession;
    capture = createEventCapture({ now, emit, describeTarget });
    capture.attach();
  }

  function stopCapturing() {
    if (!capture) return;
    capture.detach();
    capture = null;
    if (flushTimer) clearTimeout(flushTimer);
    flush();
    session = null;
  }

  chrome.runtime.onMessage.addListener((message) => {
    if (!message || message.target !== "content") return;
    if (message.type === "recording-started") startCapturing(message.session);
    if (message.type === "recording-stopped") stopCapturing();
  });

  // Navigating away destroys this context; make sure the tail of the queue still ships.
  window.addEventListener("pagehide", () => {
    if (flushTimer) clearTimeout(flushTimer);
    flush();
  });

  // The service worker may still be waking up when a new document loads, and a dropped
  // handshake would silently stop telemetry for the rest of the recording (spec 6.4).
  async function joinSession(attempt = 0) {
    const reply = await send({ target: "sw", type: "content-hello", viewport: viewport() });
    if (reply) {
      if (reply.recording) startCapturing(reply.session);
      return;
    }
    if (attempt < 3) setTimeout(() => joinSession(attempt + 1), 200 * (attempt + 1));
  }

  joinSession();
})();
