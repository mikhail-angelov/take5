// Integration tests for the RecordingLifecycle module.
// These tests verify that the new deep module works as a drop-in replacement
// for the scattered logic in service-worker.js.

import { test } from "node:test";
import assert from "node:assert";
import { RecordingLifecycle, PHASE } from "../lib/recording-lifecycle.js";

// Fake adapters for testing without Chrome APIs.

class FakePort {
  constructor() {
    this.messages = [];
    this.onMessage = new FakeEventTarget();
    this.onDisconnect = new FakeEventTarget();
    this.disconnected = false;
  }

  postMessage(msg) {
    if (this.disconnected) throw new Error("port is disconnected");
    this.messages.push(msg);
  }

  disconnect() {
    if (this.disconnected) return;
    this.disconnected = true;
    this.onDisconnect.emit();
  }

  simulateHostMessage(msg) {
    this.onMessage.emit(msg);
  }
}

class FakeEventTarget {
  constructor() {
    this.listeners = [];
  }

  addListener(fn) {
    this.listeners.push(fn);
  }

  removeListener(fn) {
    this.listeners = this.listeners.filter((l) => l !== fn);
  }

  emit(...args) {
    for (const listener of this.listeners) {
      listener(...args);
    }
  }
}

class FakeDebugger {
  constructor() {
    this.attached = new Set();
  }

  async attach(tabId) {
    this.attached.add(tabId);
  }

  async detach(tabId) {
    this.attached.delete(tabId);
  }
}

class FakeVoiceCapture {
  constructor() {
    this.started = false;
    this.shouldFail = false;
  }

  async start() {
    if (this.shouldFail) {
      return { ok: false, error: "permission denied" };
    }
    this.started = true;
    return { ok: true };
  }

  async stop() {
    this.started = false;
  }
}

class FakePersisted {
  constructor() {
    this.data = {};
  }

  async get(key) {
    return this.data[key];
  }

  async set(key, value) {
    this.data[key] = value;
  }
}

// --- Tests ---

test("happy path: start and stop recording", async () => {
  const port = new FakePort();
  const debugger_ = new FakeDebugger();
  const voice = new FakeVoiceCapture();
  const persisted = new FakePersisted();

  const lc = new RecordingLifecycle({
    port,
    debugger: debugger_,
    voiceCapture: voice,
    persisted,
  });

  assert.strictEqual(lc.phase, PHASE.IDLE);
  assert.strictEqual(lc.isRecording, false);

  // Start recording.
  const startPromise = lc.start(1, { voiceEnabled: false });

  // Simulate host accepting the session.
  setTimeout(() => {
    port.simulateHostMessage({ type: "session-accepted", dir: "/tmp/session-123" });
  }, 10);

  const result = await startPromise;
  assert.strictEqual(result.ok, true);
  assert.strictEqual(result.sessionDir, "/tmp/session-123");
  assert.strictEqual(lc.phase, PHASE.RECORDING);
  assert.strictEqual(lc.isRecording, true);
  assert.strictEqual(lc.debuggerTabId, 1);
  assert.strictEqual(debugger_.attached.has(1), true);

  // Stop recording.
  await lc.stop();
  assert.strictEqual(lc.phase, PHASE.IDLE);
  assert.strictEqual(lc.isRecording, false);
  assert.strictEqual(debugger_.attached.has(1), false);
  assert.strictEqual(port.disconnected, true);
});

test("recording with voice enabled", async () => {
  const port = new FakePort();
  const voice = new FakeVoiceCapture();

  const lc = new RecordingLifecycle({ port, voiceCapture: voice });

  const startPromise = lc.start(1, { voiceEnabled: true });
  setTimeout(() => {
    port.simulateHostMessage({ type: "session-accepted", dir: "/tmp/s" });
  }, 10);

  await startPromise;
  assert.strictEqual(voice.started, true);
  assert.strictEqual(lc.voiceActive, true);

  await lc.stop();
  assert.strictEqual(voice.started, false);
  assert.strictEqual(lc.voiceActive, false);
});

test("voice failure is additive (recording continues)", async () => {
  const port = new FakePort();
  const voice = new FakeVoiceCapture();
  voice.shouldFail = true;

  const lc = new RecordingLifecycle({ port, voiceCapture: voice });

  const startPromise = lc.start(1, { voiceEnabled: true });
  setTimeout(() => {
    port.simulateHostMessage({ type: "session-accepted", dir: "/tmp/s" });
  }, 10);

  await startPromise;
  assert.strictEqual(lc.voiceActive, false); // Voice failed.
  assert.strictEqual(lc.phase, PHASE.RECORDING); // But recording continues.

  await lc.stop();
  assert.strictEqual(lc.phase, PHASE.IDLE);
});

test("host rejection during start", async () => {
  const port = new FakePort();

  const lc = new RecordingLifecycle({ port });

  const startPromise = lc.start(1, {});
  setTimeout(() => {
    port.simulateHostMessage({ type: "error", error: "debugger attach failed" });
  }, 10);

  await assert.rejects(startPromise, /debugger attach failed/);
  assert.strictEqual(lc.phase, PHASE.IDLE);
  assert.strictEqual(port.disconnected, true);
});

test("fail() is idempotent", async () => {
  const debugger_ = new FakeDebugger();
  const lc = new RecordingLifecycle({ debugger: debugger_ });

  lc.phase = PHASE.RECORDING;
  lc.debuggerTabId = 1;
  debugger_.attached.add(1);

  // First fail.
  await lc.fail("test failure");
  assert.strictEqual(lc.phase, PHASE.IDLE);
  assert.strictEqual(debugger_.attached.has(1), false);

  // Second fail should not error.
  await lc.fail("already failed");
  assert.strictEqual(lc.phase, PHASE.IDLE);
});

test("recordFrame drops frames outside RECORDING phase", async () => {
  const port = new FakePort();
  const lc = new RecordingLifecycle({ port });

  // No frame in IDLE phase.
  await lc.recordFrame({ data: "frame" });
  assert.strictEqual(port.messages.length, 0);

  // Frame in RECORDING phase.
  lc.phase = PHASE.RECORDING;
  lc.port = port; // Ensure port is set.
  await lc.recordFrame({ data: "frame1" });
  assert.strictEqual(port.messages.length, 1);
  assert.strictEqual(port.messages[0].type, "frame");
});

test("recordAudio drops audio if voiceActive is false", async () => {
  const port = new FakePort();
  const lc = new RecordingLifecycle({ port });

  lc.phase = PHASE.RECORDING;
  lc.port = port; // Ensure port is set.
  lc.voiceActive = false;

  await lc.recordAudio({ data: "chunk" });
  assert.strictEqual(port.messages.length, 0);

  // Audio sent if voiceActive.
  lc.voiceActive = true;
  await lc.recordAudio({ data: "chunk" });
  assert.strictEqual(port.messages.length, 1);
  assert.strictEqual(port.messages[0].type, "audio-chunk");
});

test("recovery after eviction detects and cleans up stale state", async () => {
  const debugger_ = new FakeDebugger();
  const voice = new FakeVoiceCapture();
  voice.started = true;

  const lc = new RecordingLifecycle({ debugger: debugger_, voiceCapture: voice });
  lc.staleThresholdMs = 1000; // 1 second for testing.

  const staleState = {
    phase: PHASE.STARTING_CAPTURE,
    debuggerTabId: 1,
    voiceActive: true,
    createdAt: Date.now() - 5000, // 5 seconds ago = stale.
  };

  debugger_.attached.add(1);

  await lc.recoverFromEviction(staleState);
  assert.strictEqual(debugger_.attached.has(1), false); // Cleaned up.
  assert.strictEqual(voice.started, false); // Cleaned up.
});

test("persisted state is stored during start", async () => {
  const port = new FakePort();
  const persisted = new FakePersisted();
  const voice = new FakeVoiceCapture();

  const lc = new RecordingLifecycle({ port, persisted, voiceCapture: voice });

  const startPromise = lc.start(1, { voiceEnabled: true });
  setTimeout(() => {
    port.simulateHostMessage({ type: "session-accepted", dir: "/tmp/s" });
  }, 10);

  await startPromise;
  const stored = await persisted.get("recordingState");
  assert.ok(stored);
  assert.strictEqual(stored.phase, PHASE.RECORDING);
  assert.strictEqual(stored.debuggerTabId, 1);
  assert.strictEqual(stored.voiceActive, true);
});
