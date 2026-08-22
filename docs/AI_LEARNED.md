## 2026-08-17 — Compact successful capture sessions

### Goal

Keep local demo recordings re-renderable without retaining a large directory of JPEG frames.

### Golden path

1. Assemble the timestamped JPEG frames into `raw.mp4`.
2. Render and validate the finished `demo.mp4`.
3. Only after that succeeds, remove `frames/` and `frames.json`.
4. Keep `raw.mp4`, `session.json`, and `project.json`; `render` can use `raw.mp4` directly.

### Verification

`test/render.test.js` verifies that successful post-production removes the frame cache, a
subsequent re-render succeeds from `raw.mp4`, and a deliberately invalid `raw.mp4` leaves all
source frames intact after render failure.

### Failure pattern avoided

A 97-second, 5,623-frame session used 927 MB for `frames/`, while its `raw.mp4` was 7.9 MB.
Keeping both after success unnecessarily retains almost all of the session's disk usage.

### Ruled-out approaches

- Kept JPEG frames indefinitely; rejected because they are only needed to build a missing
  `raw.mp4` and dominated disk usage after successful rendering.

