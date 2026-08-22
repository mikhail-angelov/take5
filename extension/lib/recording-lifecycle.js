// Recording lifecycle module: owns recording state, phase transitions, cleanup, and recovery.
// This is the deep module that service-worker.js wraps Chrome listeners around.
//
// Interface:
//   new RecordingLifecycle(adapters)
//   .start(tabId, options) -> Promise<{ok, sessionDir}>
//   .stop() -> Promise<void>
//   .fail(reason) -> Promise<void>  // idempotent recovery on any failure
//   .recordFrame(frameData) -> Promise<void>
//   .recordAudio(chunk) -> Promise<void>
//   .phase -> string (IDLE, CONNECTING_LOCAL_HELPER, STARTING_CAPTURE, RECORDING, STOPPING)

const PHASE = {
  IDLE: "IDLE",
  CONNECTING_LOCAL_HELPER: "CONNECTING_LOCAL_HELPER",
  STARTING_CAPTURE: "STARTING_CAPTURE",
  RECORDING: "RECORDING",
  STOPPING: "STOPPING",
};

export class RecordingLifecycle {
  // adapters: { port, debugger, voiceCapture, persisted }
  // port: { postMessage(msg), disconnect(), onMessage, onDisconnect }
  // debugger: { attach(tabId), detach(tabId) }
  // voiceCapture: { start(tabId), stop(), onAudioChunk(callback) }
  // persisted: { get(key), set(key, value) }
  constructor(adapters = {}) {
    this.adapters = adapters;

    this.phase = PHASE.IDLE;
    this.sessionId = null;
    this.sessionDir = null;
    this.debuggerTabId = null;
    this.voiceEnabled = false;
    this.voiceActive = false;
    this.port = null;

    // Frame and audio relay tracking.
    this.pendingFrames = 0;
    this.pendingAudio = 0;

    // In-flight requests from extension to host.
    this.pendingRequests = new Map();
    this.nextRequestId = 1;

    // Stale phase detection (after eviction).
    this.createdAt = null;
    this.staleThresholdMs = 10000; // 10s
  }

  get isRecording() {
    return this.phase === PHASE.RECORDING;
  }

  // --- Transitions ---

  async start(tabId, options = {}) {
    if (this.phase !== PHASE.IDLE) {
      throw new Error(`cannot start from phase ${this.phase}`);
    }

    this.phase = PHASE.CONNECTING_LOCAL_HELPER;
    this.sessionId = Math.random().toString(36).substring(2, 15);
    this.createdAt = Date.now();
    this.debuggerTabId = tabId;
    this.voiceEnabled = options.voiceEnabled || false;

    try {
      // Step 1: Probe and establish native messaging port.
      if (!this.adapters.port) {
        throw new Error("port adapter required");
      }
      this.port = this.adapters.port;

      // Step 2: Transition to STARTING: attach debugger, set up screencast, start optional mic.
      this.phase = PHASE.STARTING_CAPTURE;

      if (this.adapters.debugger) {
        await this.adapters.debugger.attach(tabId);
      }

      if (this.voiceEnabled && this.adapters.voiceCapture) {
        const voiceResult = await this.adapters.voiceCapture.start();
        if (voiceResult.ok) {
          this.voiceActive = true;
        } else {
          // Voice is additive; failure does not abort recording.
          console.warn("voice capture failed, continuing with video only:", voiceResult.error);
        }
      }

      // Step 3: Wait for host to accept the session (handshake).
      // The host responds with { type: "session-accepted", dir: sessionDir }.
      const accepted = await this._waitForSessionAccepted();
      if (!accepted.ok) {
        throw new Error(`host rejected session: ${accepted.error}`);
      }

      this.sessionDir = accepted.dir;

      // Step 4: Transition to RECORDING.
      this.phase = PHASE.RECORDING;

      // Store state in persisted storage so recovery can find it on eviction.
      if (this.adapters.persisted) {
        await this.adapters.persisted.set("recordingState", {
          phase: this.phase,
          sessionId: this.sessionId,
          debuggerTabId: this.debuggerTabId,
          voiceActive: this.voiceActive,
          createdAt: this.createdAt,
        });
      }

      return { ok: true, sessionDir: this.sessionDir };
    } catch (err) {
      await this.fail(`startup failed: ${err.message}`);
      throw err;
    }
  }

  async stop() {
    if (this.phase !== PHASE.RECORDING) {
      throw new Error(`cannot stop from phase ${this.phase}`);
    }

    this.phase = PHASE.STOPPING;

    try {
      // Flush pending frames and audio before closing the port.
      await this._flushRelays();

      // Close the port gracefully.
      if (this.port) {
        this.port.disconnect();
        this.port = null;
      }

      // Clean up other resources.
      await this._cleanup();

      this.phase = PHASE.IDLE;
    } catch (err) {
      await this._cleanup();
      this.phase = PHASE.IDLE;
      throw err;
    }
  }

  async fail(reason) {
    // Idempotent: can be called multiple times from any phase.
    if (this.phase === PHASE.IDLE) {
      return;
    }

    console.warn(`recording lifecycle failed: ${reason}`);
    this.phase = PHASE.IDLE;
    await this._cleanup();
  }

  // --- Recording data ---

  async recordFrame(frameData) {
    if (!this.isRecording) {
      return; // Drop frames outside RECORDING phase.
    }

    this.pendingFrames++;
    try {
      if (this.port) {
        this.port.postMessage({ type: "frame", data: frameData });
      }
    } finally {
      this.pendingFrames--;
    }
  }

  async recordAudio(chunk) {
    if (!this.isRecording || !this.voiceActive) {
      return; // Drop audio if not recording or voice not active.
    }

    this.pendingAudio++;
    try {
      if (this.port) {
        this.port.postMessage({ type: "audio-chunk", data: chunk });
      }
    } finally {
      this.pendingAudio--;
    }
  }

  // --- Internal helpers ---

  async _waitForSessionAccepted(timeoutMs = 5000) {
    return new Promise((resolve) => {
      const timeout = setTimeout(() => {
        resolve({ ok: false, error: "session-accepted timed out" });
      }, timeoutMs);

      const onMessage = (msg) => {
        if (msg && msg.type === "session-accepted") {
          clearTimeout(timeout);
          resolve({ ok: true, dir: msg.dir });
        } else if (msg && msg.type === "error") {
          clearTimeout(timeout);
          resolve({ ok: false, error: msg.error });
        }
      };

      if (this.port && this.port.onMessage) {
        this.port.onMessage.addListener(onMessage);
      }
    });
  }

  async _flushRelays(timeoutMs = 5000) {
    const start = Date.now();
    while (this.pendingFrames > 0 || this.pendingAudio > 0) {
      if (Date.now() - start > timeoutMs) {
        console.warn("relay flush timeout");
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }

  async _cleanup() {
    // Idempotent cleanup of all resources.
    if (this.adapters.debugger && this.debuggerTabId !== null) {
      await this.adapters.debugger.detach(this.debuggerTabId).catch(() => {});
    }
    this.debuggerTabId = null;

    if (this.port) {
      this.port.disconnect();
      this.port = null;
    }

    if (this.voiceActive && this.adapters.voiceCapture) {
      await this.adapters.voiceCapture.stop().catch(() => {});
    }
    this.voiceActive = false;

    // Clear persisted state.
    if (this.adapters.persisted) {
      await this.adapters.persisted.set("recordingState", null).catch(() => {});
    }
  }

  // --- Recovery after eviction ---

  async recoverFromEviction(persistedState) {
    if (!persistedState) {
      return;
    }

    const age = Date.now() - persistedState.createdAt;
    if (age > this.staleThresholdMs) {
      // Stale state: clean it up.
      console.log("recovery: detected stale recording state, cleaning up");
      if (this.adapters.debugger && persistedState.debuggerTabId) {
        await this.adapters.debugger.detach(persistedState.debuggerTabId).catch(() => {});
      }
      if (this.adapters.voiceCapture) {
        await this.adapters.voiceCapture.stop().catch(() => {});
      }
      return;
    }

    // Non-stale state: restore (should be rare in practice, as eviction is brief).
    Object.assign(this, persistedState);
  }
}

export { PHASE };
