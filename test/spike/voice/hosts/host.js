#!/usr/bin/env node
// The passive instrument for the mic-capture spike (docs/plans/2026-08-19-voice-annotations.md
// Task 1). Same framing and same "don't attach anything, just watch stdin" discipline as
// test/spike/hosts/host.js — see that file's header for why.

import { appendFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const LOG = join(dirname(fileURLToPath(import.meta.url)), "..", "host.log");
const started = Date.now();

function log(line) {
  const dt = ((Date.now() - started) / 1000).toFixed(1);
  appendFileSync(LOG, `[+${dt.padStart(6)}s] pid=${process.pid} ${line}\n`);
}

log(`host spawned by chrome, argv=${JSON.stringify(process.argv.slice(2))}`);

let buffer = Buffer.alloc(0);
process.stdin.on("data", (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  while (buffer.length >= 4) {
    const length = buffer.readUInt32LE(0);
    if (buffer.length < 4 + length) break;
    log(`<- ${buffer.subarray(4, 4 + length).toString("utf8")}`);
    buffer = buffer.subarray(4 + length);
  }
});

process.stdin.on("end", () => {
  log("STDIN CLOSED — chrome tore the port down");
  process.exit(0);
});

for (const signal of ["SIGTERM", "SIGINT", "SIGHUP"]) {
  process.on(signal, () => {
    log(`signal ${signal}`);
    process.exit(0);
  });
}
