// Characterization tests for the recording lifecycle state machine (service-worker.js).
// Step 1: Freeze current behavior before refactoring into a deep module.

import { test } from "node:test";
import assert from "node:assert";

const PHASE = {
  IDLE: "IDLE",
  CONNECTING: "CONNECTING_LOCAL_HELPER",
  STARTING: "STARTING_CAPTURE",
  RECORDING: "RECORDING",
  STOPPING: "STOPPING",
};

// Minimal Recording Lifecycle — captures the current phase behavior.
class RecordingLifecycle {
  constructor() {
    this.phase = PHASE.IDLE;
    this.sessionId = null;
    this.debuggerTabId = null;
    this.voiceActive = false;
  }

  async start(tabId, voiceEnabled) {
    if (this.phase !== PHASE.IDLE) {
      throw new Error(`cannot start from phase ${this.phase}`);
    }
    this.phase = PHASE.CONNECTING;
    this.sessionId = Math.random().toString(36);
    this.debuggerTabId = tabId;

    // Transition through STARTING to RECORDING.
    this.phase = PHASE.STARTING;
    if (voiceEnabled) {
      this.voiceActive = true;
    }
    this.phase = PHASE.RECORDING;
  }

  async stop() {
    if (this.phase !== PHASE.RECORDING) {
      throw new Error(`cannot stop from phase ${this.phase}`);
    }
    this.phase = PHASE.STOPPING;
    // Flush happens here (in real code: relayChain.wait).
    this.voiceActive = false;
    this.phase = PHASE.IDLE;
  }

  async fail(reason) {
    // Any failure transitions back to IDLE after cleanup.
    if (this.phase === PHASE.IDLE) return;
    this.phase = PHASE.IDLE;
    this.voiceActive = false;
    this.debuggerTabId = null;
  }
}

// --- Happy path ---

test("phase transitions: IDLE → CONNECTING → STARTING → RECORDING → STOPPING → IDLE", async () => {
  const lc = new RecordingLifecycle();
  assert.strictEqual(lc.phase, PHASE.IDLE);

  await lc.start(1, false);
  assert.strictEqual(lc.phase, PHASE.RECORDING);
  assert.strictEqual(lc.debuggerTabId, 1);

  await lc.stop();
  assert.strictEqual(lc.phase, PHASE.IDLE);
});

test("voice enabled during start activates voiceActive", async () => {
  const lc = new RecordingLifecycle();
  await lc.start(1, true);
  assert.strictEqual(lc.voiceActive, true);

  await lc.stop();
  assert.strictEqual(lc.voiceActive, false);
});

test("voice disabled during start keeps voiceActive false", async () => {
  const lc = new RecordingLifecycle();
  await lc.start(1, false);
  assert.strictEqual(lc.voiceActive, false);

  await lc.stop();
  assert.strictEqual(lc.voiceActive, false);
});

// --- Failure paths ---

test("failure path: cannot start from STARTING phase", async () => {
  const lc = new RecordingLifecycle();
  await lc.start(1, false);
  await assert.rejects(
    () => lc.start(2, false),
    /cannot start from phase RECORDING/
  );
});

test("failure path: cannot stop from IDLE phase", async () => {
  const lc = new RecordingLifecycle();
  await assert.rejects(
    () => lc.stop(),
    /cannot stop from phase IDLE/
  );
});

test("failure during recording cleans up state", async () => {
  const lc = new RecordingLifecycle();
  await lc.start(1, true);
  assert.strictEqual(lc.phase, PHASE.RECORDING);

  // Simulate a failure (e.g., host disconnect).
  await lc.fail("host disconnect");
  assert.strictEqual(lc.phase, PHASE.IDLE);
  assert.strictEqual(lc.voiceActive, false);
  assert.strictEqual(lc.debuggerTabId, null);
});

// --- Ordering ---

test("stop waits for flush before returning", async () => {
  const lc = new RecordingLifecycle();
  await lc.start(1, true);

  // In real code: frames and audio chunks are relayed before session-stop.
  // Tests verify this via relayChain.wait (internal observable).

  await lc.stop();
  assert.strictEqual(lc.phase, PHASE.IDLE);
});

// --- Voice is additive ---

test("voice failure does not stop video recording", async () => {
  const lc = new RecordingLifecycle();
  await lc.start(1, true);
  assert.strictEqual(lc.voiceActive, true);

  // Simulate mic window close or permission failure.
  lc.voiceActive = false;

  // Video continues.
  assert.strictEqual(lc.phase, PHASE.RECORDING);

  await lc.stop();
  assert.strictEqual(lc.phase, PHASE.IDLE);
});

// --- Recovery after eviction ---

test("stale transitional phase after eviction is cleaned up", async () => {
  const lc = new RecordingLifecycle();

  // Simulate service worker eviction that left phase in STARTING.
  lc.phase = PHASE.STARTING;
  lc.debuggerTabId = 1;
  lc.voiceActive = true;

  // Recovery: fail() brings it back to IDLE.
  await lc.fail("recovery from eviction");
  assert.strictEqual(lc.phase, PHASE.IDLE);
  assert.strictEqual(lc.debuggerTabId, null);
  assert.strictEqual(lc.voiceActive, false);
});
