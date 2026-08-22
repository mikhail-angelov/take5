// Mic capture for opt-in voice narration (docs/plans/2026-08-19-voice-annotations.md Task 2).
// Runs in a real chrome.windows.create({type:"popup"}) window, not an offscreen document: a
// getUserMedia() permission prompt never renders inside an offscreen document — Chrome
// auto-dismisses it instead ("NotAllowedError: Permission dismissed"), confirmed against a
// real recording attempt even with the extension's origin already granted mic access from a
// separate tab. A real window shows the prompt like any site would and, unlike the
// extension's own action popup, doesn't auto-close when it loses focus, so it can stay alive
// for the whole recording the way the offscreen document was meant to.
//
// The service worker creates this window at recording start and closes it at recording stop
// (or when this document closes itself, on the happy path — see the voice-stop handler
// below), so there is only ever one MediaRecorder alive here and no cross-session state to
// guard against on this side, unlike the content script (which re-runs per navigation).
//
// connectNative does not exist in this kind of window either (same restriction SPIKE.md §1.2
// found for offscreen documents), so unlike the video path — where Page.screencastFrame goes
// straight from the service worker to the native port — audio chunks make one extra hop:
// captured here, relayed to the service worker over chrome.runtime.sendMessage, and only
// then written to the port. Verified end-to-end, including that this relay doesn't disturb a
// concurrent frame-like message stream on the same port, in test/spike/voice (see that
// spike's README for the numbers; the spike predates the offscreen-to-window switch, but the
// relay mechanism it measured is unchanged).

// Keep in sync with service-worker.js's AUDIO_MIME_TYPE constant.
const AUDIO_MIME_TYPE = "audio/webm;codecs=opus";
const CHUNK_TIMESLICE_MS = 250;
// How often the popup's level indicator refreshes. Cheap (one getByteTimeDomainData call per
// tick) and only ever consumed by a popup that's actually open — sendMessage rejects silently
// otherwise (caught below), so there's no cost when nobody is looking.
const LEVEL_METER_INTERVAL_MS = 100;

const statusEl = document.getElementById("status");
const levelEl = document.getElementById("voice-level");

const report = (payload) => chrome.runtime.sendMessage({ target: "sw", from: "mic-capture", ...payload });

// Drives the pulsing mic-level dot both on this window's own status line (the one surface
// guaranteed to be around while capture is running — the toolbar popup usually isn't open)
// and, via chrome.runtime.sendMessage, next to the popup's "Record voice narration" checkbox
// for anyone who does have it open. sendMessage reaches every listening context in the
// extension; there's no listener on the popup side when it's closed, so that send just
// rejects, caught below.
function startLevelMeter(mediaStream) {
  const ctx = new AudioContext();
  const source = ctx.createMediaStreamSource(mediaStream);
  const analyser = ctx.createAnalyser();
  analyser.fftSize = 512;
  source.connect(analyser);
  const data = new Uint8Array(analyser.frequencyBinCount);

  const timer = setInterval(() => {
    analyser.getByteTimeDomainData(data);
    let sumSquares = 0;
    for (let i = 0; i < data.length; i++) {
      const centered = (data[i] - 128) / 128;
      sumSquares += centered * centered;
    }
    // RMS is usually well under 1 for normal speech volume; scaled up so the dot's range is
    // actually visible instead of barely moving.
    const level = Math.min(1, Math.sqrt(sumSquares / data.length) * 4);
    levelEl.style.opacity = String(0.25 + level * 0.75);
    levelEl.style.transform = `scale(${0.6 + level * 0.7})`;
    chrome.runtime.sendMessage({ target: "popup", type: "voice-level", level }).catch(() => {});
  }, LEVEL_METER_INTERVAL_MS);

  return () => {
    clearInterval(timer);
    source.disconnect();
    ctx.close().catch(() => {});
  };
}

function toBase64(buf) {
  let binary = "";
  const bytes = new Uint8Array(buf);
  const chunk = 0x8000; // String.fromCharCode(...) blows the call stack on a full chunk-sized array
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

let recorder = null;
let stream = null;
let stopLevelMeter = null;
// Chunk sends are async (arrayBuffer() await); chaining through this guarantees the final
// chunk is actually sent before "voice-stopped" goes out, so the service worker never closes
// voice.webm while a chunk is still in flight.
let sendChain = Promise.resolve();

async function start() {
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  } catch (err) {
    statusEl.textContent = `Microphone access failed: ${String(err)}`;
    statusEl.className = "err";
    report({ type: "voice-error", error: String(err) });
    return;
  }

  try {
    recorder = new MediaRecorder(stream, { mimeType: AUDIO_MIME_TYPE });
  } catch (err) {
    stream.getTracks().forEach((t) => t.stop());
    statusEl.textContent = `Could not start recording: ${String(err)}`;
    statusEl.className = "err";
    report({ type: "voice-error", error: String(err) });
    return;
  }

  recorder.ondataavailable = (e) => {
    if (e.data.size === 0) return;
    sendChain = sendChain.then(async () => {
      const buf = await e.data.arrayBuffer();
      // epochMs, not performance.now(): this window's clock has no relation to the service
      // worker's, but Date.now() reads the same OS clock everywhere, which is what lets the
      // service worker convert this into the same tMs origin frames and events already use.
      report({ type: "audio-chunk", epochMs: Date.now(), data: toBase64(buf) });
    });
  };
  recorder.onerror = (e) => report({ type: "voice-error", error: String(e.error) });
  recorder.start(CHUNK_TIMESLICE_MS);
  stopLevelMeter = startLevelMeter(stream);
  statusEl.textContent = "🔴 Recording — go ahead and narrate.";
  statusEl.className = "ok";
  // epochMs marks the same instant voice.webm's first byte covers, so the service worker can
  // anchor the session clock's zero point there when voice is on — see startRecording's use of
  // it in service-worker.js.
  report({ type: "voice-ready", mimeType: AUDIO_MIME_TYPE, epochMs: Date.now() });
}

chrome.runtime.onMessage.addListener((message) => {
  if (!message || message.target !== "mic-capture") return;
  if (message.type !== "voice-stop") return;

  if (!recorder || recorder.state === "inactive") {
    report({ type: "voice-stopped" });
    window.close();
    return;
  }
  recorder.onstop = () => {
    if (stopLevelMeter) stopLevelMeter();
    stream.getTracks().forEach((t) => t.stop());
    sendChain = sendChain.then(() => {
      report({ type: "voice-stopped" });
      // service-worker.js's closeVoiceWindow() also tries to remove this window as a safety
      // net, but closing it here — right after the final chunk is confirmed sent — is the
      // primary path, so the window doesn't linger visibly for the ~seconds a round trip
      // through the service worker would otherwise take.
      window.close();
    });
  };
  recorder.stop();
});

start();
