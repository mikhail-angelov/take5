#!/usr/bin/env node
// The native-messaging half of the transport A/B (SPIKE.md §1.4). Counts frames and decoded
// bytes and reports the rate. The base64 decode is included deliberately: the host has to do
// it anyway to put a JPEG on disk, so leaving it out would flatter this path.

import { appendFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const LOG = join(dirname(fileURLToPath(import.meta.url)), "..", "bench-native.log");

let buffer = Buffer.alloc(0);
let frames = 0;
let bytes = 0;
let first = null;

// stdout is the protocol. Nothing may reach it except framed messages — the production host
// will redirect console.* to stderr for exactly this reason.
function send(message) {
  const body = Buffer.from(JSON.stringify(message), "utf8");
  const header = Buffer.alloc(4);
  header.writeUInt32LE(body.length, 0);
  process.stdout.write(Buffer.concat([header, body]));
}

process.stdin.on("data", (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  while (buffer.length >= 4) {
    const length = buffer.readUInt32LE(0);
    if (buffer.length < 4 + length) break;
    const message = JSON.parse(buffer.subarray(4, 4 + length).toString("utf8"));
    buffer = buffer.subarray(4 + length);

    if (message.data) {
      if (first === null) first = Date.now();
      frames += 1;
      bytes += Buffer.from(message.data, "base64").length;
    } else if (message.type === "report") {
      appendFileSync(
        LOG,
        "report:    sender-side cost in the service worker — " +
          `native ${message.nativeSendMs} ms, websocket-via-offscreen ${message.wsSendMs} ms\n`,
      );
    } else if (message.type === "done") {
      // The report arrives over a second port, i.e. a second host process, which received no
      // frames of its own and has nothing to summarise.
      if (first === null) {
        send({ type: "ack" });
        continue;
      }
      const elapsed = Date.now() - first;
      appendFileSync(
        LOG,
        `native:    ${frames} frames, ${(bytes / 1e6).toFixed(1)} MB in ${elapsed} ms = ` +
          `${(bytes / 1e6 / (elapsed / 1000)).toFixed(1)} MB/s\n`,
      );
      send({ type: "ack" });
    }
  }
});
