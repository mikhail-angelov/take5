## 2026-07-31 — Current CLI produces replay artifacts only

### Goal

Prepare a Take5 capture bundle for a product screencast.

### Golden path

1. Create a schema-v1 capture JSON with `metadata`, `steps`, `annotations`, and `debug`.
2. Run `node src/cli.js run <capture-file> --out <directory> --cut-delays`.
3. Use the generated run directory's `capture.json` and `plan.json` as the replay handoff.

### Verification

`fixtures/easycad-screencast-capture.json` completed successfully and produced both artifacts in `take5-output/easycad/`.

### Failure pattern avoided

Do not expect the current `run` command to open a browser or produce `.webm`/`.mp4`; `src/commands/run.js` currently validates, normalizes, and writes artifacts only.

### Ruled-out approaches

- Tried using `node src/cli.js run ... --cut-delays` as a video-render command; it created only `capture.json` and `plan.json` because no replay runtime is wired into the command.

### Notes

The product design describes Playwright replay and video rendering, but the checked-in implementation has not reached that stage.
