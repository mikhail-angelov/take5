const STATUS_LABEL = {
  ready: "Ready",
  processing: "Processing",
  render_failed: "Render failed",
  recording_failed: "Recording failed",
};

const listEl = document.getElementById("list");
const refreshBtn = document.getElementById("refresh");

function formatDuration(ms) {
  const totalSeconds = Math.round((ms || 0) / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function renderSessions(sessions) {
  listEl.textContent = "";
  if (!sessions.length) {
    listEl.textContent = "No recordings yet.";
    return;
  }

  for (const s of sessions) {
    const row = document.createElement("div");
    row.className = "session";

    const main = document.createElement("div");
    main.className = "session-main";

    const status = document.createElement("span");
    status.className = `status status-${s.status}`;
    status.textContent = STATUS_LABEL[s.status] || s.status;
    main.appendChild(status);

    const url = document.createElement("span");
    url.className = "url";
    url.textContent = s.url || "(unknown url)";
    main.appendChild(url);

    row.appendChild(main);

    const meta = document.createElement("div");
    meta.className = "session-meta";
    const started = s.startedAtEpochMs ? new Date(s.startedAtEpochMs).toLocaleString() : "unknown time";
    meta.textContent = `${started} · ${formatDuration(s.durationMs)} · ${s.dir}`;
    row.appendChild(meta);

    if (s.reason) {
      const reason = document.createElement("div");
      reason.className = "reason";
      reason.textContent = s.reason;
      row.appendChild(reason);
    }

    listEl.appendChild(row);
  }
}

async function load() {
  listEl.textContent = "Loading…";
  const result = await chrome.runtime.sendMessage({ target: "sw", type: "list-sessions" });
  if (!result || !result.ok) {
    listEl.textContent = "";
    const err = document.createElement("div");
    err.className = "error";
    err.textContent = `Could not reach the take5 host (${(result && result.error) || "unknown error"}).`;
    listEl.appendChild(err);
    return;
  }
  renderSessions(result.sessions);
}

refreshBtn.addEventListener("click", load);
load();
