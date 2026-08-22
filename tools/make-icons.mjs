#!/usr/bin/env node
// Regenerates extension/icons/*.png. Run only when the icon design changes.

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { encodePng } from "./png.js";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const OUT_DIR = join(ROOT, "extension", "icons");
const SIZES = [16, 32, 48, 128];

const SUPERSAMPLE = 4;
const BACKGROUND = [24, 28, 36];
const DOT = [214, 61, 51];

// Coverage of a rounded square, sampled with `SUPERSAMPLE`^2 points per pixel.
function roundedSquareCoverage(px, py, size, radius) {
  const inset = size * 0.06;
  const min = inset;
  const max = size - inset;
  const r = radius;
  const cx = Math.min(Math.max(px, min + r), max - r);
  const cy = Math.min(Math.max(py, min + r), max - r);
  if (px < min || px > max || py < min || py > max) return 0;
  return Math.hypot(px - cx, py - cy) <= r ? 1 : 0;
}

function render(size) {
  const rgba = new Uint8Array(size * size * 4);
  const radius = size * 0.22;
  const dotRadius = size * 0.26;
  const centre = size / 2;
  const step = 1 / SUPERSAMPLE;

  for (let y = 0; y < size; y += 1) {
    for (let x = 0; x < size; x += 1) {
      let bg = 0;
      let dot = 0;
      for (let sy = 0; sy < SUPERSAMPLE; sy += 1) {
        for (let sx = 0; sx < SUPERSAMPLE; sx += 1) {
          const px = x + (sx + 0.5) * step;
          const py = y + (sy + 0.5) * step;
          bg += roundedSquareCoverage(px, py, size, radius);
          dot += Math.hypot(px - centre, py - centre) <= dotRadius ? 1 : 0;
        }
      }
      const samples = SUPERSAMPLE * SUPERSAMPLE;
      bg /= samples;
      dot /= samples;

      const alpha = Math.max(bg, dot);
      const i = (y * size + x) * 4;
      for (let c = 0; c < 3; c += 1) {
        rgba[i + c] = Math.round(BACKGROUND[c] * (1 - dot) + DOT[c] * dot);
      }
      rgba[i + 3] = Math.round(alpha * 255);
    }
  }
  return encodePng(size, size, rgba);
}

mkdirSync(OUT_DIR, { recursive: true });
for (const size of SIZES) {
  const file = join(OUT_DIR, `icon-${size}.png`);
  writeFileSync(file, render(size));
  console.log(`wrote ${file}`);
}
