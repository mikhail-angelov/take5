#!/usr/bin/env node
// The passive instrument for the service-worker lifetime question (SPIKE.md §1.1).
//
// It measures without observing the browser: Chrome closes this process's stdin the instant
// it tears the port down, so the log's last line is the moment the port — and therefore the
// service worker holding it — went away. Nothing here attaches to anything, which matters,
// because attaching a debugger to a service worker keeps it alive and would void the result.

import { appendFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const LOG = join(dirname(fileURLToPath(import.meta.url)), "..", "host.log");
const started = Date.now();

function log(line) {
  const dt = ((Date.now() - started) / 1000).toFixed(1);
  appendFileSync(LOG, `[+${dt.padStart(6)}s] pid=${process.pid} ${line}\n`);
}

// argv[1] is the calling origin: Chrome hands the host `chrome-extension://<id>/`.
log(`host spawned by chrome, argv=${JSON.stringify(process.argv.slice(2))}`);
// Which interpreter actually resolved, and out of which PATH. A host registered through a
// `#!/usr/bin/env node` shebang depends on this PATH containing the user's node, and Chrome
// started from the Dock does not inherit a shell's PATH (SPIKE.md §1.5).
log(`interpreter=${process.execPath}`);
log(`PATH=${process.env.PATH}`);

// Length-prefixed JSON: 4-byte little-endian length, then UTF-8 JSON.
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

setInterval(() => log("alive"), 5000);

for (const signal of ["SIGTERM", "SIGINT", "SIGHUP"]) {
  process.on(signal, () => {
    log(`signal ${signal}`);
    process.exit(0);
  });
}
