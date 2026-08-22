// Turns raw DOM events into the small, privacy-preserving event set described in spec 12.
//
// No text values, no selectors, no replay data — only what post-production needs to decide
// where the cursor should be, where the camera should look, and which pauses are real.

(() => {
  const POINTER_HZ = 20;
  const POINTER_MIN_INTERVAL_MS = 1000 / POINTER_HZ;
  const POINTER_MIN_DISTANCE_PX = 4;

  const DRAG_MIN_DISTANCE_PX = 8;
  const DRAG_MIN_DURATION_MS = 120;

  const INPUT_MIN_INTERVAL_MS = 100;
  const SCROLL_IDLE_MS = 150;

  const NAMED_KEYS = new Set([
    "Enter",
    "Escape",
    "Tab",
    "ArrowUp",
    "ArrowDown",
    "ArrowLeft",
    "ArrowRight",
  ]);

  function distance(ax, ay, bx, by) {
    return Math.hypot(ax - bx, ay - by);
  }

  function shortcutName(e) {
    const parts = [];
    if (e.metaKey) parts.push("Meta");
    if (e.ctrlKey) parts.push("Control");
    if (e.altKey) parts.push("Alt");
    if (e.shiftKey && (parts.length || NAMED_KEYS.has(e.key))) parts.push("Shift");

    const hasModifier = e.metaKey || e.ctrlKey || e.altKey;
    if (!hasModifier && !NAMED_KEYS.has(e.key)) return null;

    // Printable key values are never recorded; only their modifier combination (spec 12.5).
    const key = NAMED_KEYS.has(e.key)
      ? e.key
      : e.key.length === 1
        ? e.key.toUpperCase()
        : e.key;
    parts.push(key);
    return parts.join("+");
  }

  // `now()` returns ms since recording start; `emit(event)` queues one session event.
  function createEventCapture({ now, emit, describeTarget }) {
    let attached = false;
    const listeners = [];

    let lastPointerAt = -Infinity;
    let lastPointerX = null;
    let lastPointerY = null;

    let pointerDown = null;
    // The last pointerdown that hasn't yet been claimed by a click or turned into a drag.
    // Not cleared by onPointerUp on its own — DOM guarantees pointerdown -> pointerup -> click
    // order, so onClick always sees the value set by the pointerdown that led to it. Known
    // edge case, left unhandled on purpose: if pointerup fires but the synthetic click never
    // does (e.g. the target element is removed/replaced between the two), this value survives
    // and attaches itself to the next real click as a wrong startMs. Rare, low-cost (an
    // inaccurate span, not a broken recording) — no TTL or target-matching added for it.
    let lastPressDown = null;
    let lastInputAt = new Map();
    let scroll = null;
    let scrollTimer = null;

    function samplePointer(x, y, t) {
      if (t - lastPointerAt < POINTER_MIN_INTERVAL_MS) return false;
      if (
        lastPointerX !== null &&
        distance(x, y, lastPointerX, lastPointerY) < POINTER_MIN_DISTANCE_PX
      ) {
        return false;
      }
      lastPointerAt = t;
      lastPointerX = x;
      lastPointerY = y;
      return true;
    }

    function onPointerMove(e) {
      const t = now();
      if (pointerDown) {
        // During a drag the path itself is meaningful, so sample it unconditionally
        // at the pointer rate rather than dropping small movements.
        if (t - lastPointerAt >= POINTER_MIN_INTERVAL_MS) {
          lastPointerAt = t;
          lastPointerX = e.clientX;
          lastPointerY = e.clientY;
          pointerDown.path.push({ t, x: e.clientX, y: e.clientY });
        }
        return;
      }
      if (!samplePointer(e.clientX, e.clientY, t)) return;
      emit({ t, kind: "pointer", x: e.clientX, y: e.clientY });
    }

    function onPointerDown(e) {
      if (e.button !== 0 || e.pointerType === "touch") return;
      const t = now();
      pointerDown = { t, x: e.clientX, y: e.clientY, path: [{ t, x: e.clientX, y: e.clientY }] };
      lastPressDown = { t, x: e.clientX, y: e.clientY };
    }

    function onPointerUp(e) {
      const down = pointerDown;
      pointerDown = null;
      if (!down) return;

      const t = now();
      const moved = distance(down.x, down.y, e.clientX, e.clientY);
      if (moved < DRAG_MIN_DISTANCE_PX || t - down.t < DRAG_MIN_DURATION_MS) return;

      // The span is already covered by the drag record below; onClick must not duplicate it.
      lastPressDown = null;

      const path = down.path.concat([{ t, x: e.clientX, y: e.clientY }]);
      emit({
        t: down.t,
        kind: "drag",
        startMs: down.t,
        endMs: t,
        x: e.clientX,
        y: e.clientY,
        path,
      });
    }

    function onClick(e) {
      // A drag ends with a click event too; the drag record already covers it.
      if (!e.isTrusted) return;
      const t = now();
      const event = { t, kind: "click", x: e.clientX, y: e.clientY, button: e.button };
      if (lastPressDown) {
        event.startMs = lastPressDown.t;
        event.endMs = t;
        lastPressDown = null;
      }
      const target = describeTarget(e.target);
      if (target) event.target = target;
      emit(event);
    }

    function onInput(e) {
      const el = e.target;
      if (!el || el.nodeType !== 1) return;
      const t = now();
      const last = lastInputAt.get(el);
      if (last !== undefined && t - last < INPUT_MIN_INTERVAL_MS) return;
      lastInputAt.set(el, t);

      const event = { t, kind: "input" };
      const target = describeTarget(el);
      if (target) event.target = target;
      emit(event);
    }

    function onKeyDown(e) {
      if (!e.isTrusted) return;
      const key = shortcutName(e);
      if (!key) return;
      emit({ t: now(), kind: "shortcut", key });
    }

    function closeScrollGesture() {
      scrollTimer = null;
      if (!scroll) return;
      const gesture = scroll;
      scroll = null;
      emit({
        t: gesture.startMs,
        kind: "scroll",
        startMs: gesture.startMs,
        endMs: gesture.endMs,
        deltaY: Math.round(gesture.deltaY),
      });
    }

    function onWheel(e) {
      const t = now();
      if (!scroll) scroll = { startMs: t, endMs: t, deltaY: 0 };
      scroll.endMs = t;
      scroll.deltaY += e.deltaY;
      if (scrollTimer) clearTimeout(scrollTimer);
      scrollTimer = setTimeout(closeScrollGesture, SCROLL_IDLE_MS);
    }

    function on(target, type, handler, options) {
      target.addEventListener(type, handler, options);
      listeners.push(() => target.removeEventListener(type, handler, options));
    }

    function attach() {
      if (attached) return;
      attached = true;
      const capture = { capture: true, passive: true };
      on(document, "pointermove", onPointerMove, capture);
      on(document, "pointerdown", onPointerDown, capture);
      on(document, "pointerup", onPointerUp, capture);
      on(document, "click", onClick, capture);
      on(document, "input", onInput, capture);
      on(document, "keydown", onKeyDown, capture);
      on(document, "wheel", onWheel, capture);
    }

    function detach() {
      if (!attached) return;
      attached = false;
      if (scrollTimer) clearTimeout(scrollTimer);
      closeScrollGesture();
      while (listeners.length) listeners.pop()();
      lastInputAt = new Map();
      lastPressDown = null;
    }

    return { attach, detach };
  }

  globalThis.__demoRecorderEventCapture = { createEventCapture, shortcutName, NAMED_KEYS };
})();
