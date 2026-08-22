// Writes the throwaway "voice-mic" extension this spike drives. Shares the same pinned key
// (and therefore extension id) as the Spike 1 extensions in test/spike/, via the same
// keys.mjs — one id is all any of these spikes ever need, and there's no reason to mint a
// second one just because this harness lives in its own directory.

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { ensureKey } from "../keys.mjs";

const here = dirname(fileURLToPath(import.meta.url));
export const HOST_NAME = "com.demo_recorder.voice_spike";

function write(dir, files) {
  mkdirSync(join(here, "extensions", dir), { recursive: true });
  for (const [name, content] of Object.entries(files)) {
    writeFileSync(join(here, "extensions", dir, name), content);
  }
}

export function buildExtension() {
  const { key, id } = ensureKey();

  const manifest = JSON.stringify(
    {
      manifest_version: 3,
      name: "Spike — voice mic capture",
      version: "0.1.0",
      key,
      minimum_chrome_version: "116",
      background: { service_worker: "sw.js" },
      permissions: ["nativeMessaging", "offscreen"],
    },
    null,
    2,
  );

  // SPIKE.md §1.2 already established chrome.runtime.connectNative does not exist inside an
  // offscreen document — the function itself is undefined there. So the native port lives in
  // the service worker, exactly as it does for video, and the offscreen document (required
  // here because the service worker cannot call getUserMedia at all) relays audio chunks up
  // to the service worker over chrome.runtime.sendMessage, the same hop the old WebSocket
  // frame path used before it was deleted for video. The question this spike answers is
  // whether that relay — now carrying audio instead of video frames — coexists on the same
  // native port as other traffic without destabilizing it.
  write("voice-mic", {
    "manifest.json": manifest,
    "sw.js": `
const port = chrome.runtime.connectNative("${HOST_NAME}");
port.onDisconnect.addListener(() => {
  const err = chrome.runtime.lastError;
  if (err) console.log("[voice-spike-sw] port disconnected:", err.message);
});
port.postMessage({ hello: "service-worker", at: Date.now() });

chrome.runtime.onMessage.addListener((message) => {
  if (message && message.target === "sw") port.postMessage({ relayed: message });
});

chrome.offscreen
  .createDocument({
    url: "offscreen.html",
    reasons: ["USER_MEDIA"],
    justification: "spike: mic capture reachability + relay to native port",
  })
  .then(() => port.postMessage({ offscreen: "created" }))
  .catch((err) => port.postMessage({ offscreen: "create failed", error: String(err) }));

// Synthetic stand-in for the real video frame stream (SPIKE.md §1.4's own port), sent
// concurrently on the SAME port the audio relay also writes to. If audio relay traffic were
// going to starve or reorder the frame stream, it would show up here as gaps or reordering
// in the host's log, not just as a subjective "seems fine".
let frame = 0;
const frameTimer = setInterval(() => {
  port.postMessage({ syntheticFrame: frame, at: Date.now() });
  frame += 1;
  if (frame >= 120) {
    clearInterval(frameTimer);
    port.postMessage({ syntheticFramesDone: true });
  }
}, 33);

setTimeout(() => port.postMessage({ spikeDone: true }), 6000);
`,
    "offscreen.html": '<!doctype html><meta charset="utf-8"><script src="offscreen.js"></script>',
    "offscreen.js": `
const report = (payload) => chrome.runtime.sendMessage({ target: "sw", from: "offscreen", ...payload });

function toBase64(buf) {
  let binary = "";
  const bytes = new Uint8Array(buf);
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

async function main() {
  // Answers "can permission be requested once, not re-prompted every recording": query the
  // permission state before AND after getUserMedia. If it reads "granted" up front on a
  // second offscreen-document lifecycle within the same profile, the grant persisted on the
  // extension's origin rather than needing to be re-asked — that's the mechanical half of
  // the question. Whether Chrome's actual permission bubble appears at all under
  // --use-fake-ui-for-media-stream is a separate, real-Chrome-only question this harness
  // deliberately can't answer (see README's caveat).
  let before = "unknown";
  try {
    before = (await navigator.permissions.query({ name: "microphone" })).state;
  } catch (err) {
    before = "query threw: " + err;
  }
  report({ event: "permission-before", state: before });

  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    report({ event: "getUserMedia resolved", tracks: stream.getAudioTracks().length });
  } catch (err) {
    report({ event: "getUserMedia rejected", error: String(err) });
    return;
  }

  try {
    const after = (await navigator.permissions.query({ name: "microphone" })).state;
    report({ event: "permission-after", state: after });
  } catch (err) {
    report({ event: "permission-after query threw", error: String(err) });
  }

  let recorder;
  try {
    recorder = new MediaRecorder(stream, { mimeType: "audio/webm;codecs=opus" });
  } catch (err) {
    report({ event: "MediaRecorder construction failed", error: String(err) });
    return;
  }

  const started = performance.now();
  let seq = 0;
  recorder.ondataavailable = async (e) => {
    if (e.data.size === 0) return;
    const buf = await e.data.arrayBuffer();
    report({
      event: "audio-chunk",
      seq: seq++,
      tMs: Math.round(performance.now() - started),
      bytes: buf.byteLength,
      // Only the size and a short prefix travel to the log — full base64 would bloat
      // host.log for no reason, the relay mechanism is what's under test, not the bytes.
      dataPrefix: toBase64(buf).slice(0, 24),
    });
  };
  recorder.onerror = (e) => report({ event: "recorder-error", error: String(e.error) });
  recorder.onstart = () => report({ event: "recorder-started" });
  recorder.start(250);

  setTimeout(() => {
    recorder.stop();
    stream.getTracks().forEach((t) => t.stop());
    report({ event: "recorder-stopped" });
  }, 5000);
}

main();
`,
  });

  return { id };
}
