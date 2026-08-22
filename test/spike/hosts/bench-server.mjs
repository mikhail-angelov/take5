// The WebSocket half of the transport A/B (SPIKE.md §1.4). It stands in for
// src/receiver/server.js and reads the same way — JSON header, then binary payload — so the
// comparison is against the transport the project actually has, not an idealised one.

import { WebSocketServer } from "ws";
import { appendFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const LOG = join(dirname(fileURLToPath(import.meta.url)), "..", "bench-ws.log");
export const BENCH_PORT = 47824;

const wss = new WebSocketServer({ host: "127.0.0.1", port: BENCH_PORT, path: "/bench" });

wss.on("connection", (ws) => {
  let frames = 0;
  let bytes = 0;
  let first = null;

  ws.on("message", (data, isBinary) => {
    if (!isBinary) {
      if (JSON.parse(data.toString("utf8")).type === "done") {
        const elapsed = Date.now() - first;
        appendFileSync(
          LOG,
          `websocket: ${frames} frames, ${(bytes / 1e6).toFixed(1)} MB in ${elapsed} ms = ` +
            `${(bytes / 1e6 / (elapsed / 1000)).toFixed(1)} MB/s\n`,
        );
      }
      return;
    }
    if (first === null) first = Date.now();
    frames += 1;
    bytes += data.length;
  });
});
