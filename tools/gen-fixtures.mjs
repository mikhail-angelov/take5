// Generates the Phase 0 fixture corpus: real session.json files covering the scenarios
// named in docs/plans/go-port.md (fast typing, a long pause inside one field, a network
// wait, scrolling, a drag, a navigation, and a recording with no actions at all).
//
// These are frozen inputs: the golden project.json/pass-a.filter/pass-b.filter/overlay.ass
// alongside each session.json are the Go implementation's regression baseline
// (internal/render's golden_test.go). Do not edit an existing fixture's events — add a new
// scenario instead, or the golden files it was compared against no longer mean anything.

import { mkdir, writeFile } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

import {
  VIEWPORT,
  click,
  drag,
  input,
  makeSession,
  rect,
  scroll,
  shortcut,
} from "../test/fixtures/session.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const CORPUS_DIR = join(__dirname, "..", "test", "fixtures", "corpus");

function request({ id, startMs, endMs, type = "xmlhttprequest", status = 200 }) {
  return { id, startMs, endMs, type, status, failed: false };
}

const fixtures = {
  "fast-typing": makeSession({
    events: [
      click({ t: 500, x: 420, y: 220, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      input({ t: 800, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      input({ t: 950, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      input({ t: 1080, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      input({ t: 1230, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      input({ t: 1400, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      input({ t: 1600, label: "Search", role: "textbox", rect: rect(360, 200, 320, 40) }),
      click({ t: 2000, x: 700, y: 220, label: "Submit", role: "button", rect: rect(660, 200, 100, 40) }),
    ],
    durationMs: 2800,
  }),

  "long-pause-in-field": makeSession({
    events: [
      click({ t: 500, x: 420, y: 300, label: "Message", role: "textbox", rect: rect(300, 260, 500, 120) }),
      input({ t: 800, label: "Message", role: "textbox", rect: rect(300, 260, 500, 120) }),
      input({ t: 1000, label: "Message", role: "textbox", rect: rect(300, 260, 500, 120) }),
      input({ t: 3500, label: "Message", role: "textbox", rect: rect(300, 260, 500, 120) }),
      input({ t: 3700, label: "Message", role: "textbox", rect: rect(300, 260, 500, 120) }),
      click({ t: 4200, x: 760, y: 400, label: "Send", role: "button", rect: rect(720, 380, 100, 40) }),
    ],
    durationMs: 5000,
  }),

  "network-wait": makeSession({
    events: [
      click({ t: 1000, x: 670, y: 422, label: "Generate", role: "button", rect: rect(600, 400, 140, 44) }),
      click({ t: 4500, x: 700, y: 520, label: "Result", role: "link", rect: rect(640, 500, 220, 32) }),
    ],
    network: [request({ id: "1", startMs: 1050, endMs: 4200, type: "xmlhttprequest" })],
    durationMs: 5500,
  }),

  scrolling: makeSession({
    events: [
      click({ t: 500, x: 200, y: 150, label: "Article", role: "link", rect: rect(120, 120, 300, 60) }),
      scroll({ startMs: 1200, endMs: 2400, deltaY: 1800 }),
      scroll({ startMs: 3000, endMs: 3600, deltaY: -400 }),
      click({ t: 4200, x: 500, y: 700, label: "Comment", role: "button", rect: rect(440, 680, 160, 40) }),
    ],
    durationMs: 5000,
  }),

  drag: makeSession({
    events: [
      click({ t: 500, x: 300, y: 300, label: "Card", role: "listitem", rect: rect(260, 280, 120, 40) }),
      drag({
        startMs: 1000,
        endMs: 2200,
        path: [
          { x: 300, y: 300, t: 1000 },
          { x: 500, y: 280, t: 1400 },
          { x: 700, y: 260, t: 1800 },
          { x: 900, y: 240, t: 2200 },
        ],
      }),
      click({ t: 3200, x: 900, y: 240, label: "Confirm", role: "button", rect: rect(860, 220, 100, 40) }),
    ],
    durationMs: 4000,
  }),

  navigation: makeSession({
    events: [
      click({ t: 800, x: 720, y: 60, label: "Dashboard", role: "link", rect: rect(200, 30, 900, 60) }),
      click({ t: 5600, x: 400, y: 300, label: "Widget", role: "button", rect: rect(340, 260, 200, 80) }),
    ],
    network: [request({ id: "1", startMs: 850, endMs: 5200, type: "main_frame" })],
    durationMs: 6200,
  }),

  "no-actions": makeSession({
    events: [],
    durationMs: 3000,
  }),

  hesitation: makeSession({
    events: [
      click({ t: 1000, x: 300, y: 300, label: "Menu", role: "button", rect: rect(260, 280, 100, 40) }),
      click({ t: 3500, x: 300, y: 400, label: "Settings", role: "menuitem", rect: rect(260, 380, 120, 30) }),
    ],
    durationMs: 4500,
  }),

  "shortcut-and-mixed": makeSession({
    events: [
      click({ t: 500, x: 1216, y: 56, label: "Settings", role: "button", rect: rect(1200, 40, 32, 32) }),
      shortcut({ t: 1200, key: "Meta+K" }),
      input({ t: 2000, label: "Search", role: "textbox", rect: rect(400, 240, 300, 40) }),
      input({ t: 2300, label: "Search", role: "textbox", rect: rect(400, 240, 300, 40) }),
      scroll({ startMs: 3200, endMs: 3800, deltaY: 900 }),
      click({ t: 4500, x: 700, y: 450, label: "Generate", role: "button", rect: rect(200, 300, 900, 300) }),
    ],
    durationMs: 5500,
  }),
};

async function main() {
  await mkdir(CORPUS_DIR, { recursive: true });
  for (const [name, session] of Object.entries(fixtures)) {
    const dir = join(CORPUS_DIR, name);
    await mkdir(dir, { recursive: true });
    session.viewport = VIEWPORT;
    await writeFile(join(dir, "session.json"), `${JSON.stringify(session, null, 2)}\n`);
    process.stdout.write(`wrote ${join(dir, "session.json")}\n`);
  }
}

main();
