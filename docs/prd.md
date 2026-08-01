# Take5 PRD

> **Superseded.** The original prompt-first PRD (an autonomous LLM agent that
> explored a live app and recorded video) has been retired. Take5 is now an
> **extension-first** capture → replay → render product.

## Canonical documents

- **Product design (extension-first):**
  [`docs/superpowers/specs/2026-05-25-extension-first-take5-v0-1-0-design.md`](./superpowers/specs/2026-05-25-extension-first-take5-v0-1-0-design.md)
- **Direction, cleanup, and target video-render architecture:**
  [`docs/2026-08-01-cleanup-and-direction-spec.md`](./2026-08-01-cleanup-and-direction-spec.md)

## One-paragraph summary

A user manually performs a target flow in their web app while the Take5 Chrome
extension records semantic actions and inline annotations. The extension exports
the capture as JSON. The `take5` CLI normalizes the capture into a deterministic
replay plan and (roadmap) replays it in Playwright, rendering a polished,
annotated demo video via [`playwright-recast`](https://github.com/ThePatriczek/playwright-recast).
Flow discovery is human-driven; recording and polish are machine-driven, and the
demo re-renders in CI so it never goes stale.
