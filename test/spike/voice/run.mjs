#!/usr/bin/env node
// The mic-capture feasibility spike for docs/plans/2026-08-19-voice-annotations.md Task 1.
// Not part of `npm test`, same reasoning as ../run.mjs: it launches a real Chrome and answers
// a question that only needs re-answering if Chrome changes underneath the project.
//
//   node test/spike/voice/run.mjs
//
// --use-fake-device-for-media-stream + --use-fake-ui-for-media-stream make this
// deterministic and headless-safe: Chrome synthesizes a fake audio input device and
// auto-grants the getUserMedia prompt instead of waiting on a human. That means this harness
// cannot observe what the *permission bubble itself* looks like on a first real run — see
// README.md's caveat — only whether the mechanism (offscreen capture, relay, persisted grant
// across an offscreen-document lifecycle) works at all.

import { spawn } from "node:child_process";
import { mkdirSync, rmSync, writeFileSync, readFileSync, existsSync, chmodSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { buildExtension, HOST_NAME } from "./build.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const CDP_PORT = 9334; // distinct from ../run.mjs's 9333 so both can run independently

const t0 = Date.now();
const say = (line) => process.stdout.write(`[+${((Date.now() - t0) / 1000).toFixed(1).padStart(6)}s] ${line}\n`);

function hostManifest(profile, { name, path, id }) {
  mkdirSync(join(profile, "NativeMessagingHosts"), { recursive: true });
  chmodSync(path, 0o755);
  writeFileSync(
    join(profile, "NativeMessagingHosts", `${name}.json`),
    JSON.stringify(
      { name, description: "take5 voice spike host", path, type: "stdio", allowed_origins: [`chrome-extension://${id}/`] },
      null,
      2,
    ),
  );
}

async function browserWsUrl() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const { webSocketDebuggerUrl } = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/version`)).json();
      if (webSocketDebuggerUrl) return webSocketDebuggerUrl;
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error("chrome never exposed a debugging endpoint");
}

function cdp(ws, id, method, params) {
  return new Promise((resolve) => {
    const onMessage = (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id !== id) return;
      ws.removeEventListener("message", onMessage);
      resolve(msg.result || msg.error);
    };
    ws.addEventListener("message", onMessage);
    ws.send(JSON.stringify({ id, method, params }));
  });
}

function launchChrome(profile) {
  return spawn(
    CHROME,
    [
      `--user-data-dir=${profile}`,
      `--remote-debugging-port=${CDP_PORT}`,
      "--no-first-run",
      "--no-default-browser-check",
      // Deterministic, headless-safe microphone: a synthesized device + auto-accepted prompt.
      "--use-fake-device-for-media-stream",
      "--use-fake-ui-for-media-stream",
      "--window-size=700,500",
      "--window-position=1400,900",
      "about:blank",
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
}

function summarize(logText) {
  const lines = logText.split("\n").filter((l) => l.includes("<- "));
  const events = [];
  for (const line of lines) {
    const jsonStart = line.indexOf("<- ") + 3;
    try {
      events.push(JSON.parse(line.slice(jsonStart)));
    } catch {
      // non-JSON log line, ignore
    }
  }

  const relayed = events.filter((e) => e.relayed).map((e) => e.relayed);
  const audioChunks = relayed.filter((e) => e.event === "audio-chunk");
  const syntheticFrames = events.filter((e) => Number.isInteger(e.syntheticFrame)).map((e) => e.syntheticFrame);
  const permBefore = relayed.find((e) => e.event === "permission-before");
  const permAfter = relayed.find((e) => e.event === "permission-after");
  const gUM = relayed.find((e) => e.event === "getUserMedia resolved" || e.event === "getUserMedia rejected");
  const offscreen = events.find((e) => "offscreen" in e);

  let framesInOrder = true;
  for (let i = 1; i < syntheticFrames.length; i += 1) {
    if (syntheticFrames[i] !== syntheticFrames[i - 1] + 1) framesInOrder = false;
  }
  const frameGapsMs = [];
  const frameEvents = events.filter((e) => Number.isInteger(e.syntheticFrame));
  for (let i = 1; i < frameEvents.length; i += 1) frameGapsMs.push(frameEvents[i].at - frameEvents[i - 1].at);
  const maxFrameGap = frameGapsMs.length ? Math.max(...frameGapsMs) : null;

  return {
    offscreenCreated: offscreen ? offscreen.offscreen : "(no offscreen message seen)",
    getUserMedia: gUM || "(no getUserMedia event seen)",
    permissionBefore: permBefore ? permBefore.state : "(not seen)",
    permissionAfter: permAfter ? permAfter.state : "(not seen)",
    audioChunkCount: audioChunks.length,
    audioTotalBytes: audioChunks.reduce((sum, c) => sum + (c.bytes || 0), 0),
    syntheticFrameCount: syntheticFrames.length,
    syntheticFramesInOrder: framesInOrder,
    maxFrameGapMs: maxFrameGap,
    sawStdinClosed: logText.includes("STDIN CLOSED"),
  };
}

async function run() {
  const { id } = buildExtension();
  const profile = join(here, "profile-voice-mic");
  const log = join(here, "host.log");

  rmSync(profile, { recursive: true, force: true });
  rmSync(log, { force: true });
  mkdirSync(profile, { recursive: true });
  hostManifest(profile, { name: HOST_NAME, path: join(here, "hosts", "host.js"), id });

  const chrome = launchChrome(profile);
  say(`chrome pid ${chrome.pid}, extension id ${id}`);

  const ws = new WebSocket(await browserWsUrl());
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));

  const loaded = await cdp(ws, 1, "Extensions.loadUnpacked", { path: join(here, "extensions", "voice-mic") });
  say(`Extensions.loadUnpacked -> ${JSON.stringify(loaded)}`);

  say("waiting 7s for capture + relay + synthetic frame stream to finish...");
  await new Promise((r) => setTimeout(r, 7000));

  chrome.kill("SIGTERM");
  await new Promise((r) => setTimeout(r, 1000));

  const logText = existsSync(log) ? readFileSync(log, "utf8") : "";
  process.stdout.write("\n--- host.log ---\n");
  process.stdout.write(logText || "(host was never spawned)\n");

  const summary = summarize(logText);
  process.stdout.write("\n--- summary ---\n");
  process.stdout.write(JSON.stringify(summary, null, 2) + "\n");
}

if (!existsSync(CHROME)) {
  process.stderr.write(`Chrome not found at ${CHROME}\n`);
  process.exit(1);
}

await run();
process.exit(0);
