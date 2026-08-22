#!/usr/bin/env node
// Serves the manual acceptance app (spec 30.5). A static file:// page is not enough: the
// pipeline needs a real slow XHR for chrome.webRequest to time.

import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { dirname, extname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = dirname(fileURLToPath(import.meta.url));
const PORT = Number(process.env.PORT) || 8899;
const TYPES = { ".html": "text/html; charset=utf-8", ".json": "application/json" };

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);

  if (url.pathname === "/api/generate") {
    // Deliberately slow, so the Director has a network wait worth compressing.
    const delayMs = Math.min(Number(url.searchParams.get("ms")) || 5000, 30000);
    await new Promise((resolve) => setTimeout(resolve, delayMs));
    res.writeHead(200, { "content-type": TYPES[".json"] });
    res.end(JSON.stringify({ id: `run-${Date.now().toString(36)}`, delayMs }));
    return;
  }

  const name = url.pathname === "/" ? "/app.html" : url.pathname;
  try {
    const body = await readFile(join(ROOT, name.replace(/^\/+/, "")));
    res.writeHead(200, { "content-type": TYPES[extname(name)] || "application/octet-stream" });
    res.end(body);
  } catch {
    res.writeHead(404, { "content-type": "text/plain" });
    res.end("not found");
  }
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`acceptance app: http://127.0.0.1:${PORT}/app.html`);
});
