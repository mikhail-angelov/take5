// Builders for synthetic raw sessions, so Director tests read like the scenario they test.

export const VIEWPORT = { width: 1440, height: 900, devicePixelRatio: 2 };

export function rect(x, y, width, height) {
  return { x, y, width, height };
}

export function click({ t, x, y, label, role = "button", rect: r }) {
  const event = { t, kind: "click", x, y, button: 0 };
  if (r) event.target = { role, rect: r, ...(label ? { label } : {}) };
  return event;
}

export function input({ t, label, role = "textbox", rect: r }) {
  return { t, kind: "input", target: { role, rect: r, ...(label ? { label } : {}) } };
}

export function shortcut({ t, key }) {
  return { t, kind: "shortcut", key };
}

export function scroll({ startMs, endMs, deltaY }) {
  return { t: startMs, kind: "scroll", startMs, endMs, deltaY };
}

export function drag({ startMs, endMs, path }) {
  const last = path[path.length - 1];
  return { t: startMs, kind: "drag", startMs, endMs, x: last.x, y: last.y, path };
}

export function pointer({ t, x, y }) {
  return { t, kind: "pointer", x, y };
}

export function request({ id = "1", startMs, endMs, type = "xmlhttprequest", status = 200 }) {
  return { id, startMs, endMs, type, status, failed: false };
}

export function makeSession({ events = [], network = [], durationMs, viewport = VIEWPORT } = {}) {
  const lastEvent = events.reduce((max, e) => Math.max(max, e.endMs ?? e.t), 0);
  return {
    version: 1,
    sessionId: "test-session",
    startedAtEpochMs: 1786957200123,
    durationMs: durationMs ?? lastEvent + 1000,
    url: "https://app.example.com/dashboard",
    viewport,
    capture: {},
    events: [...events].sort((a, b) => a.t - b.t),
    network,
  };
}
