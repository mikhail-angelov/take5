#!/usr/bin/env node
// Spike 1 harness — the experiments behind SPIKE.md §1. Not part of `npm test`: it launches a
// real Chrome, takes minutes, and answers questions that only need re-answering when Chrome
// changes underneath the project.
//
//   node test/spike/run.mjs lifetime [seconds]   does a native port keep the worker alive?
//   node test/spike/run.mjs control  [seconds]   the same extension without a port
//   node test/spike/run.mjs offscreen            is connectNative reachable from offscreen?
//   node test/spike/run.mjs bench                native vs websocket, same payload
//   node test/spike/run.mjs dock                 shebang host under the GUI PATH (expect FAIL)
//   node test/spike/run.mjs dock-fixed           launcher-wrapped host, same PATH (expect pass)
//   node test/spike/run.mjs dock-go              compiled Go host, same PATH (expect pass —
//                                                 docs/plans/go-port.md Phase 1 gate)
//   node test/spike/run.mjs all                  all four, in order
//
// Everything runs against a throwaway --user-data-dir, so the real Chrome profile is never
// touched — including its NativeMessagingHosts directory.

import { spawn, spawnSync } from "node:child_process";
import { mkdirSync, rmSync, writeFileSync, readFileSync, existsSync, chmodSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { buildExtensions, HOST_NAME, BENCH_HOST_NAME } from "./build.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const CDP_PORT = 9333;

const logPath = (name) => join(here, name);
const say = (t0, line) =>
  process.stdout.write(`[+${((Date.now() - t0) / 1000).toFixed(1).padStart(6)}s] ${line}\n`);

function hostManifest(profile, { name, path, id }) {
  // On macOS and Linux Chrome looks for host manifests inside the user data dir, which is
  // what keeps this experiment out of the real profile. On Windows it is a registry key.
  mkdirSync(join(profile, "NativeMessagingHosts"), { recursive: true });
  chmodSync(path, 0o755);
  writeFileSync(
    join(profile, "NativeMessagingHosts", `${name}.json`),
    JSON.stringify(
      { name, description: "take5 spike host", path, type: "stdio", allowed_origins: [`chrome-extension://${id}/`] },
      null,
      2,
    ),
  );
}

async function browserWsUrl() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const { webSocketDebuggerUrl } = await (
        await fetch(`http://127.0.0.1:${CDP_PORT}/json/version`)
      ).json();
      if (webSocketDebuggerUrl) return webSocketDebuggerUrl;
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error("chrome never exposed a debugging endpoint");
}

const CHROME_ARGS = (profile) => [
  `--user-data-dir=${profile}`,
  `--remote-debugging-port=${CDP_PORT}`,
  "--no-first-run",
  "--no-default-browser-check",
  "--window-size=700,500",
  "--window-position=1400,900",
  "about:blank",
];

function launchChrome(profile) {
  return spawn(CHROME, CHROME_ARGS(profile), { stdio: ["ignore", "pipe", "pipe"] });
}

// The same browser, started the way a user starts it. A Chrome spawned from a shell inherits
// that shell's PATH and will happily resolve an nvm or homebrew interpreter that a Chrome
// started from the Dock cannot see — which makes every host-spawning result from the runs
// above a false positive for the question in SPIKE.md §1.5. `launchctl getenv PATH` is
// normally unset, so GUI apps get the system default; this reproduces exactly that.
const GUI_PATH = "/usr/bin:/bin:/usr/sbin:/sbin";

function launchChromeAsGuiApp(profile) {
  return spawn("open", ["-na", "Google Chrome", "--args", ...CHROME_ARGS(profile)], {
    stdio: "inherit",
    env: { PATH: GUI_PATH, HOME: process.env.HOME },
  });
}

// Chrome 151 ignores --load-extension outright — the switch is accepted, nothing loads, and
// nothing is logged. --disable-features=DisableLoadExtensionCommandLineSwitch no longer
// revives it. Extensions.loadUnpacked over CDP is the supported route (SPIKE.md §1).
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

// Watches service workers appear and disappear *without attaching to them*. Attaching keeps a
// worker alive — that is what DevTools does — and would void the lifetime experiment.
function watchWorkers(ws, t0) {
  const seen = new Map();
  ws.addEventListener("message", (event) => {
    const msg = JSON.parse(event.data);
    const info = msg.params && msg.params.targetInfo;
    if (msg.method === "Target.targetCreated" && info && info.type === "service_worker") {
      seen.set(info.targetId, info.url);
      say(t0, `service worker STARTED  ${info.url}`);
    }
    if (msg.method === "Target.targetDestroyed" && seen.has(msg.params.targetId)) {
      say(t0, `service worker EVICTED  ${seen.get(msg.params.targetId)}`);
      seen.delete(msg.params.targetId);
    }
  });
  return cdp(ws, 1, "Target.setDiscoverTargets", { discover: true });
}

// What `take5 install` will have to generate: the interpreter resolved to an absolute
// path while a shell's environment is still available, rather than looked up on a PATH the
// host will not have.
function writeLauncher() {
  const path = join(here, "hosts", "host-launcher.sh");
  writeFileSync(
    path,
    `#!/bin/sh\nexec "${process.execPath}" "${join(here, "hosts", "host.js")}" "$@"\n`,
  );
  chmodSync(path, 0o755);
  return path;
}

// docs/plans/go-port.md, Phase 1 gate: a Go binary has no interpreter, so it should spawn
// directly under the restricted GUI PATH that defeats the `#!/usr/bin/env node` shebang in
// dock (SPIKE.md §1.5) — no launcher, unlike writeLauncher() above. cmd/spike-host is Go's
// counterpart to hosts/host.js: same log format, same question.
function buildGoHost() {
  const path = join(here, "hosts", "spike-host");
  const repoRoot = join(here, "..", "..");
  const result = spawnSync("go", ["build", "-o", path, "./cmd/spike-host"], { cwd: repoRoot, stdio: "inherit" });
  if (result.status !== 0) throw new Error("go build ./cmd/spike-host failed");
  return path;
}

async function lifetimeRun(extension, seconds, { gui = false, hostPath } = {}) {
  const t0 = Date.now();
  const { id } = buildExtensions();
  const profile = join(here, `profile-${extension}`);
  const log = logPath("host.log");

  rmSync(profile, { recursive: true, force: true });
  rmSync(log, { force: true });
  mkdirSync(profile, { recursive: true });
  hostManifest(profile, { name: HOST_NAME, path: hostPath || join(here, "hosts", "host.js"), id });

  const chrome = gui ? launchChromeAsGuiApp(profile) : launchChrome(profile);
  say(t0, `chrome ${gui ? "started as a GUI app" : `pid ${chrome.pid}`}, extension ${extension}, id ${id}`);

  const ws = new WebSocket(await browserWsUrl());
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));
  await watchWorkers(ws, t0);
  say(t0, "observer attached to the browser target (not to any worker)");

  const loaded = await cdp(ws, 2, "Extensions.loadUnpacked", {
    path: join(here, "extensions", extension),
  });
  say(t0, `Extensions.loadUnpacked → ${JSON.stringify(loaded)}`);
  say(t0, `waiting ${seconds}s in complete silence…`);
  await new Promise((r) => setTimeout(r, seconds * 1000));

  const alive = existsSync(log) && !readFileSync(log, "utf8").includes("STDIN CLOSED");
  say(t0, `before killing chrome: host port ${alive ? "STILL OPEN" : "closed / never opened"}`);

  // `open` returns immediately, so the GUI-launched browser is not this process's child.
  if (gui) spawn("pkill", ["-f", `user-data-dir=${profile}`], { stdio: "ignore" });
  else chrome.kill("SIGTERM");
  await new Promise((r) => setTimeout(r, 1500));
  process.stdout.write("\n--- host.log ---\n");
  process.stdout.write(existsSync(log) ? readFileSync(log, "utf8") : "(host was never spawned)\n");
}

async function benchRun() {
  const t0 = Date.now();
  const { id } = buildExtensions();
  const profile = join(here, "profile-bench");
  const nativeLog = logPath("bench-native.log");
  const wsLog = logPath("bench-ws.log");

  rmSync(profile, { recursive: true, force: true });
  rmSync(nativeLog, { force: true });
  rmSync(wsLog, { force: true });
  mkdirSync(profile, { recursive: true });
  hostManifest(profile, { name: BENCH_HOST_NAME, path: join(here, "hosts", "bench-host.js"), id });

  const server = spawn("node", [join(here, "hosts", "bench-server.mjs")], { stdio: "inherit" });
  await new Promise((r) => setTimeout(r, 700));

  const chrome = launchChrome(profile);
  const ws = new WebSocket(await browserWsUrl());
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));
  say(t0, `Extensions.loadUnpacked → ${JSON.stringify(
    await cdp(ws, 1, "Extensions.loadUnpacked", { path: join(here, "extensions", "bench") }),
  )}`);

  for (let attempt = 0; attempt < 240; attempt += 1) {
    const native = existsSync(nativeLog) ? readFileSync(nativeLog, "utf8") : "";
    const websocket = existsSync(wsLog) ? readFileSync(wsLog, "utf8") : "";
    if (native.includes("report") && websocket.includes("websocket")) break;
    await new Promise((r) => setTimeout(r, 500));
  }
  await new Promise((r) => setTimeout(r, 1000));

  process.stdout.write("\n--- results ---\n");
  process.stdout.write(existsSync(nativeLog) ? readFileSync(nativeLog, "utf8") : "(no native log)\n");
  process.stdout.write(existsSync(wsLog) ? readFileSync(wsLog, "utf8") : "(no websocket log)\n");

  chrome.kill("SIGTERM");
  server.kill("SIGTERM");
}

const [command = "all", secondsArg] = process.argv.slice(2);
const seconds = Number(secondsArg);

if (!existsSync(CHROME)) {
  process.stderr.write(`Chrome not found at ${CHROME}\n`);
  process.exit(1);
}

switch (command) {
  case "lifetime":
    await lifetimeRun("lifetime", seconds || 330);
    break;
  case "control":
    await lifetimeRun("control", seconds || 60);
    break;
  case "offscreen":
    await lifetimeRun("offscreen", seconds || 30);
    break;
  case "bench":
    await benchRun();
    break;
  // The host registered by its shebang, against a Chrome with the GUI environment. Expected
  // to FAIL — that is the finding (SPIKE.md §1.5), and a pass here means either the machine
  // has node on the system PATH or launchctl carries a custom one.
  case "dock":
    await lifetimeRun("lifetime", seconds || 15, { gui: true });
    break;
  // The same run with a launcher that hardcodes this interpreter. Expected to spawn.
  case "dock-fixed":
    await lifetimeRun("lifetime", seconds || 15, { gui: true, hostPath: writeLauncher() });
    break;
  // docs/plans/go-port.md Phase 1 gate: the host manifest points straight at a compiled Go
  // binary, no launcher. Expected to spawn, for the same reason dock-fixed does — nothing
  // here depends on PATH — except this time there is no interpreter to hardcode at all.
  case "dock-go":
    await lifetimeRun("lifetime", seconds || 15, { gui: true, hostPath: buildGoHost() });
    break;
  case "all":
    await lifetimeRun("lifetime", 330);
    await lifetimeRun("control", 60);
    await lifetimeRun("offscreen", 30);
    await benchRun();
    await lifetimeRun("lifetime", 15, { gui: true });
    await lifetimeRun("lifetime", 15, { gui: true, hostPath: writeLauncher() });
    break;
  default:
    process.stderr.write(`unknown spike: ${command}\n`);
    process.exit(1);
}

process.exit(0);
