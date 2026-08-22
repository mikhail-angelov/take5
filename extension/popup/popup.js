const PHASE_LABEL = {
  IDLE: "Idle",
  CONNECTING_LOCAL_HELPER: "Connecting…",
  STARTING_CAPTURE: "Starting…",
  RECORDING: "Recording",
  STOPPING: "Finishing…",
};

const PHASE_DETAIL = {
  IDLE: "Click Start to record the active tab.",
  CONNECTING_LOCAL_HELPER: "Connecting to the local take5 host.",
  STARTING_CAPTURE: "Attaching the debugger and starting the screencast.",
  RECORDING: "Recording the active tab. Click Stop when you're done.",
  STOPPING: "Wrapping up and handing the recording to the local host.",
};

const phaseEl = document.getElementById("phase");
const detailEl = document.getElementById("detail");
const toggleBtn = document.getElementById("toggle");
const historyBtn = document.getElementById("open-history");
const voiceEnabledInput = document.getElementById("voice-enabled");
const voiceLevelEl = document.getElementById("voice-level");
const voiceStatusEl = document.getElementById("voice-status");

// Keep in sync with service-worker.js's VOICE_STORAGE_KEY.
const VOICE_STORAGE_KEY = "voiceEnabled";

let state = { phase: "IDLE" };

function render() {
  const phase = state.phase || "IDLE";
  phaseEl.textContent = PHASE_LABEL[phase] || phase;
  phaseEl.className = "phase" + (phase === "RECORDING" ? " recording" : "") + (phase !== "IDLE" && phase !== "RECORDING" ? " busy" : "");
  detailEl.textContent = PHASE_DETAIL[phase] || "";

  if (phase === "RECORDING") {
    toggleBtn.textContent = "Stop recording";
    toggleBtn.classList.add("stop");
    toggleBtn.disabled = false;
  } else if (phase === "IDLE") {
    toggleBtn.textContent = "Start recording";
    toggleBtn.classList.remove("stop");
    toggleBtn.disabled = false;
  } else {
    toggleBtn.textContent = PHASE_LABEL[phase] || phase;
    toggleBtn.classList.remove("stop");
    toggleBtn.disabled = true;
  }

  // Changing the toggle mid-recording wouldn't affect the recording already in progress
  // (the service worker reads it once, at start), so it's disabled outside IDLE to avoid
  // implying otherwise.
  voiceEnabledInput.disabled = phase !== "IDLE";

  // session.audio is only set by the service worker once voice capture actually started
  // (service-worker.js's `audio: voiceActive ? {...} : undefined`), so this reflects a
  // working mic, not just the checkbox — no dot without real capture to show a level for.
  const voiceLive = phase === "RECORDING" && !!(state.session && state.session.audio);
  voiceLevelEl.classList.toggle("active", voiceLive);
  if (!voiceLive) setVoiceLevel(0);

  // Reflects a mic failure from the just-finished/current recording (service-worker.js's
  // `voiceFailure`, set alongside `notify()` there) so a popup opened after the fact — or
  // one that missed the desktop notification — still shows why there's no level dot.
  // Left alone outside RECORDING/STOPPING so it doesn't clobber the checkbox handler's own
  // status message from a click that hasn't led to a recording yet.
  if (phase === "RECORDING" || phase === "STOPPING") {
    setVoiceStatus(state.voiceFailure ? `Voice narration didn't start: ${state.voiceFailure}` : "");
  }
}

function setVoiceLevel(level) {
  const clamped = Math.max(0, Math.min(1, level));
  voiceLevelEl.style.opacity = String(0.25 + clamped * 0.75);
  voiceLevelEl.style.transform = `scale(${0.6 + clamped * 0.7})`;
}

function setVoiceStatus(text) {
  voiceStatusEl.textContent = text;
  voiceStatusEl.classList.toggle("visible", !!text);
}

async function refresh() {
  state = (await chrome.runtime.sendMessage({ target: "sw", type: "get-state" })) || state;
  render();
}

toggleBtn.addEventListener("click", async () => {
  toggleBtn.disabled = true;
  if (state.phase === "IDLE") {
    await chrome.runtime.sendMessage({ target: "sw", type: "start-recording" });
  } else if (state.phase === "RECORDING") {
    await chrome.runtime.sendMessage({ target: "sw", type: "stop-recording" });
  }
  refresh();
});

historyBtn.addEventListener("click", () => {
  chrome.tabs.create({ url: chrome.runtime.getURL("history/history.html") });
  window.close();
});

// Sent directly by mic-capture.js (not relayed through the service worker, which has no use
// for it) whenever the popup is open and voice capture is running. Ignored while the dot
// isn't "active" — render() already zeroed it, and phase can flip mid-flight between messages.
chrome.runtime.onMessage.addListener((message) => {
  if (!message || message.target !== "popup" || message.type !== "voice-level") return;
  if (!voiceLevelEl.classList.contains("active")) return;
  setVoiceLevel(message.level);
});

// Keeps the popup live if it stays open across a phase change (e.g. the operator watches it
// through STARTING_CAPTURE -> RECORDING), without polling.
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "session" && changes.state) {
    state = changes.state.newValue || { phase: "IDLE" };
    render();
  }
  // Reflects a change made from another open popup instance, if any.
  if (area === "local" && changes[VOICE_STORAGE_KEY]) {
    voiceEnabledInput.checked = changes[VOICE_STORAGE_KEY].newValue === true;
  }
});

// Awaited (and Start disabled meanwhile) so a Start click can't race the write and read the
// old stored value — the checkbox would show one thing while the recording started with
// another. Just a preference here — no mic permission check: that only works from the
// mic-capture window itself (see service-worker.js's startVoiceCapture), which doesn't exist
// until an actual recording starts.
voiceEnabledInput.addEventListener("change", async () => {
  toggleBtn.disabled = true;
  try {
    setVoiceStatus("");
    await chrome.storage.local.set({ [VOICE_STORAGE_KEY]: voiceEnabledInput.checked });
  } finally {
    if (state.phase === "IDLE") toggleBtn.disabled = false;
  }
});

async function loadVoiceToggle() {
  const stored = await chrome.storage.local.get(VOICE_STORAGE_KEY);
  voiceEnabledInput.checked = stored[VOICE_STORAGE_KEY] === true;
}

render();
refresh();
loadVoiceToggle();
