// Writes the four throwaway extensions the spike drives. They share one pinned key, and so
// one id, which is why each run gets its own profile and they are never loaded together.

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { ensureKey } from "./keys.mjs";

const here = dirname(fileURLToPath(import.meta.url));
export const HOST_NAME = "com.demo_recorder.spike";
export const BENCH_HOST_NAME = "com.demo_recorder.bench";

const FRAMES = 300;
// 400k base64 chars ≈ a 300 KB JPEG, which is what a 2560×1600 q85 screencast frame weighs.
const BASE64_CHARS = 400000;

function write(dir, files) {
  mkdirSync(join(here, "extensions", dir), { recursive: true });
  for (const [name, content] of Object.entries(files)) {
    writeFileSync(join(here, "extensions", dir, name), content);
  }
}

export function buildExtensions() {
  const { key, id } = ensureKey();

  const manifest = (name, extra) =>
    JSON.stringify(
      {
        manifest_version: 3,
        name,
        version: "0.1.0",
        key,
        minimum_chrome_version: "116",
        background: { service_worker: "sw.js" },
        ...extra,
      },
      null,
      2,
    );

  // --- lifetime: one native port, opened once, then total silence --------------------------
  write("lifetime", {
    "manifest.json": manifest("Spike — native port held", { permissions: ["nativeMessaging"] }),
    "sw.js": `
// Opened at top level, so it happens on every service-worker start. If Chrome evicts the
// worker the port dies with it, and the host sees stdin close — which is the measurement.
const port = chrome.runtime.connectNative("${HOST_NAME}");
port.onDisconnect.addListener(() => {});
port.postMessage({ hello: "service-worker", at: Date.now() });
`,
  });

  // --- control: identical, minus the port --------------------------------------------------
  write("control", {
    "manifest.json": manifest("Spike — control, no native port", { permissions: [] }),
    "sw.js": `console.log("[spike-control] service worker started at", Date.now());`,
  });

  // --- offscreen: is connectNative reachable from an offscreen document at all? ------------
  write("offscreen", {
    "manifest.json": manifest("Spike — offscreen native port", {
      permissions: ["nativeMessaging", "offscreen"],
    }),
    "sw.js": `
// The worker's own port is only a reporting channel: whatever the offscreen document finds
// out has to reach the host log somehow.
const port = chrome.runtime.connectNative("${HOST_NAME}");
port.onDisconnect.addListener(() => {});
port.postMessage({ hello: "service-worker", at: Date.now() });

chrome.runtime.onMessage.addListener((message) => port.postMessage({ relayed: message }));

chrome.offscreen
  .createDocument({
    url: "offscreen.html",
    reasons: ["BLOBS"],
    justification: "spike: test connectNative availability in an offscreen document",
  })
  .then(() => port.postMessage({ offscreen: "created" }))
  .catch((err) => port.postMessage({ offscreen: "create failed", error: String(err) }));
`,
    "offscreen.html": '<!doctype html><meta charset="utf-8"><script src="offscreen.js"></script>',
    "offscreen.js": `
const report = (payload) => chrome.runtime.sendMessage({ from: "offscreen", ...payload });
report({ typeofConnectNative: typeof chrome.runtime.connectNative });
try {
  const port = chrome.runtime.connectNative("${HOST_NAME}");
  port.postMessage({ hello: "offscreen", at: Date.now() });
  report({ event: "connect returned a port" });
} catch (err) {
  report({ event: "connectNative threw", error: String(err) });
}
`,
  });

  // --- bench: the same payload down both transports, back to back --------------------------
  write("bench", {
    "manifest.json": manifest("Spike — transport benchmark", {
      permissions: ["nativeMessaging", "offscreen"],
    }),
    "sw.js": `
const FRAMES = ${FRAMES};
const CHARS = ${BASE64_CHARS};

function payload() {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  let out = "";
  while (out.length < CHARS) out += alphabet[(Math.random() * 64) | 0];
  return out;
}

async function benchNative(data) {
  const port = chrome.runtime.connectNative("${BENCH_HOST_NAME}");
  const done = new Promise((resolve) => port.onMessage.addListener(resolve));
  const started = performance.now();
  for (let i = 0; i < FRAMES; i += 1) port.postMessage({ tMs: i * 33, data });
  const sent = performance.now() - started;
  port.postMessage({ type: "done" });
  await done;
  port.disconnect();
  return sent;
}

// Mirrors extension/offscreen.js exactly, including the hop into the offscreen document,
// which exists only because a WebSocket has to outlive service-worker eviction.
async function benchWebSocket(data) {
  await chrome.offscreen.createDocument({
    url: "offscreen.html",
    reasons: ["BLOBS"],
    justification: "spike: websocket transport benchmark",
  });
  await new Promise((r) => setTimeout(r, 500));
  const started = performance.now();
  for (let i = 0; i < FRAMES; i += 1) {
    await chrome.runtime.sendMessage({ target: "offscreen", type: "frame", tMs: i * 33, data });
  }
  const sent = performance.now() - started;
  await chrome.runtime.sendMessage({ target: "offscreen", type: "done" });
  return sent;
}

(async () => {
  const data = payload();
  const nativeSendMs = Math.round(await benchNative(data));
  const wsSendMs = Math.round(await benchWebSocket(data));

  const port = chrome.runtime.connectNative("${BENCH_HOST_NAME}");
  port.postMessage({ type: "report", nativeSendMs, wsSendMs });
  port.postMessage({ type: "done" });
})();
`,
    "offscreen.html": '<!doctype html><meta charset="utf-8"><script src="offscreen.js"></script>',
    "offscreen.js": `
let ws = null;
const ready = new Promise((resolve) => {
  ws = new WebSocket("ws://127.0.0.1:47824/bench");
  ws.binaryType = "arraybuffer";
  ws.onopen = resolve;
});

// The fetch/data: round trip is what extension/offscreen.js does today.
async function decode(base64) {
  const response = await fetch("data:application/octet-stream;base64," + base64);
  return response.arrayBuffer();
}

let chain = Promise.resolve();
chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (!message || message.target !== "offscreen") return;
  chain = chain.then(async () => {
    await ready;
    if (message.type === "frame") {
      const bytes = await decode(message.data);
      ws.send(JSON.stringify({ type: "frame", tMs: message.tMs, bytes: bytes.byteLength }));
      ws.send(bytes);
    } else if (message.type === "done") {
      ws.send(JSON.stringify({ type: "done" }));
    }
  });
  chain.then(() => sendResponse({ ok: true }));
  return true;
});
`,
  });

  return { id };
}
