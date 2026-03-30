import { chromium } from "playwright";
import {
  injectAnnotation,
  clearAnnotation,
  showEndAnnotation,
} from "./annotator.js";
import path from "path";
import fs from "fs/promises";

export class Browser {
  constructor() {
    this.browser = null;
    this.context = null;
    this.page = null;
    this.recording = false;
    this.webmPath = null;
    this.recordingStartTime = null;
    this.firstActionTime = null;
    this.actionSegments = []; // Array of {start, end} timestamps for meaningful actions
    this.currentSegmentStart = null;
    this.cutDelays = false; // Whether to cut delays between actions
  }

  async launch({
    outputDir,
    viewport = { width: 1280, height: 720 },
  }) {
    await fs.mkdir(outputDir, { recursive: true });

    this.browser = await chromium.launch({
      headless: false,
      args: ["--start-maximized"],
    });

    this.context = await this.browser.newContext({
      viewport,
      recordVideo: {
        dir: outputDir,
        size: viewport,
      },
    });

    this.page = await this.context.newPage();
    this.recording = true;
    this.recordingStartTime = Date.now();
    this.firstActionTime = null;
    return this.page;
  }

  /**
   * Returns all MCP-compatible tool definitions for the DeepSeek agent.
   * These mirror what microsoft/playwright-mcp exposes, plus our custom annotation tools.
   */
  getToolDefinitions() {
    return [
      {
        type: "function",
        function: {
          name: "browser_snapshot",
          description:
            "Capture the current page accessibility snapshot. Returns a structured tree of all interactive and visible elements with their refs, roles, labels, and states. Always call this before deciding what to interact with.",
          parameters: { type: "object", properties: {} },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_navigate",
          description: "Navigate to a URL.",
          parameters: {
            type: "object",
            properties: {
              url: { type: "string", description: "The URL to navigate to." },
            },
            required: ["url"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_click",
          description: "Click an element on the page.",
          parameters: {
            type: "object",
            properties: {
              ref: {
                type: "string",
                description: "Element ref from snapshot (e.g. e42)",
              },
              selector: {
                type: "string",
                description: "CSS selector fallback if no ref",
              },
            },
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_drag",
          description:
            "Drag on an element by pressing the mouse, moving, and releasing. Use this for canvas drawing, sliders, and other pointer-drag interactions.",
          parameters: {
            type: "object",
            properties: {
              ref: {
                type: "string",
                description: "Element ref from snapshot (e.g. e42)",
              },
              selector: {
                type: "string",
                description: "CSS selector fallback if no ref",
              },
              startX: {
                type: "number",
                description:
                  "Drag start X in pixels relative to the target element's top-left corner",
              },
              startY: {
                type: "number",
                description:
                  "Drag start Y in pixels relative to the target element's top-left corner",
              },
              endX: {
                type: "number",
                description:
                  "Drag end X in pixels relative to the target element's top-left corner",
              },
              endY: {
                type: "number",
                description:
                  "Drag end Y in pixels relative to the target element's top-left corner",
              },
            },
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_fill",
          description: "Type text into an input field.",
          parameters: {
            type: "object",
            properties: {
              ref: { type: "string", description: "Element ref from snapshot" },
              selector: {
                type: "string",
                description: "CSS selector fallback",
              },
              value: { type: "string", description: "Text to type" },
            },
            required: ["value"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_select",
          description: "Select an option from a dropdown.",
          parameters: {
            type: "object",
            properties: {
              ref: { type: "string", description: "Element ref from snapshot" },
              selector: {
                type: "string",
                description: "CSS selector fallback",
              },
              value: {
                type: "string",
                description: "Option value or label to select",
              },
            },
            required: ["value"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_hover",
          description: "Hover over an element to reveal tooltips or dropdowns.",
          parameters: {
            type: "object",
            properties: {
              ref: { type: "string", description: "Element ref from snapshot" },
              selector: {
                type: "string",
                description: "CSS selector fallback",
              },
            },
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_scroll",
          description: "Scroll the page or an element.",
          parameters: {
            type: "object",
            properties: {
              direction: {
                type: "string",
                enum: ["up", "down"],
                description: "Scroll direction",
              },
              amount: { type: "number", description: "Pixels to scroll" },
              selector: {
                type: "string",
                description: "Element to scroll (optional, defaults to page)",
              },
            },
            required: ["direction", "amount"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_wait",
          description:
            "Wait for a specified duration (use sparingly — mainly after navigations or animations).",
          parameters: {
            type: "object",
            properties: {
              ms: { type: "number", description: "Milliseconds to wait" },
            },
            required: ["ms"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "browser_press_key",
          description: "Press a keyboard key (e.g. Enter, Tab, Escape).",
          parameters: {
            type: "object",
            properties: {
              key: {
                type: "string",
                description: "Key name (e.g. Enter, Tab, Escape, ArrowDown)",
              },
            },
            required: ["key"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "inject_annotation",
          description:
            "Show a visual annotation overlay in the recording: an orange highlight ring around the target element and a tooltip with the description. Call this BEFORE each meaningful user action.",
          parameters: {
            type: "object",
            properties: {
              description: {
                type: "string",
                description:
                  'Human-readable description shown as tooltip (e.g. "Creating a new project")',
              },
              selector: {
                type: "string",
                description:
                  "Optional CSS selector of the element to highlight",
              },
            },
            required: ["description"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "clear_annotation",
          description:
            "Remove the current annotation overlay. Call after each action completes.",
          parameters: { type: "object", properties: {} },
        },
      },
      {
        type: "function",
        function: {
          name: "finish_recording",
          description:
            "Signal that the demo scenario is complete. Call this only when the full scenario has been demonstrated.",
          parameters: {
            type: "object",
            properties: {
              summary: {
                type: "string",
                description: "Brief summary of what was demonstrated",
              },
            },
            required: ["summary"],
          },
        },
      },
      {
        type: "function",
        function: {
          name: "show_end_annotation",
          description:
            'Show "The End" annotation with confetti animation. Call this at the very end of the demo scenario.',
          parameters: { type: "object", properties: {} },
        },
      },
    ];
  }

  addSegment(){
      if (
        this.cutDelays &&
        this.currentSegmentStart !== null &&
        !this.actionSegments.find(
          ({ start }) => start === this.currentSegmentStart,
        )
      ) {
        this.actionSegments.push({
          start: this.currentSegmentStart,
          end: Date.now(),
        });
      }
  }

  /**
   * Execute a tool call from the agent and return the result string.
   */
  async executeTool(name, args) {
    const p = this.page;

    // Track start of meaningful action
    const isMeaningfulAction = [
      "browser_click",
      "browser_drag",
      "browser_fill",
      "browser_select",
      "browser_hover",
      "browser_scroll",
      "browser_press_key",
      "inject_annotation",
      "show_end_annotation",
    ].includes(name);

    if (this.cutDelays) {
      this.addSegment();
      if (isMeaningfulAction) {
        // Start new segment
        this.currentSegmentStart = Date.now();
      }
    }

    switch (name) {
      case "browser_snapshot": {
        // Use the new _snapshotForAI API (Playwright 1.44+)
        try {
          const result = await p._snapshotForAI({ interestingOnly: true });
          if (!result || !result.full) {
            return "Page is empty or not yet loaded.";
          }

          // Parse the snapshot string to populate refMap for clickByRef
          parseSnapshotForRefMap(result.full);

          // Return the formatted snapshot
          return result.full;
        } catch (error) {
          console.error("Snapshot error:", error);
          return `Snapshot failed: ${error.message}`;
        }
      }

      case "browser_navigate": {
        await p.goto(args.url, {
          waitUntil: "domcontentloaded",
          timeout: 15000,
        });
        await p.waitForTimeout(50); // Further reduced from 200ms to 50ms
        return `Navigated to ${args.url}`;
      }

      case "browser_click": {
        try {
          await highlightClickTarget(p, args);

          if (args.ref) {
            // First try to find element by contenteditable attribute (for CodeMirror editors)
            const node = _refMap.get(args.ref);
            if (node) {
              // Try different approaches for contenteditable elements
              if (isContentEditableNode(node)) {
                // Try to find contenteditable element
                await p
                  .locator('[contenteditable="true"]')
                  .first()
                  .click({ timeout: 3000 })
                  .catch(async () => {
                    // Fall back to finding by role and name
                    await clickByRef(p, args.ref);
                  });
              } else {
                // Use aria snapshot ref if available
                await p
                  .locator(`[data-ref="${args.ref}"]`)
                  .click({ timeout: 3000 })
                  .catch(async () => {
                    // Fall back to finding by snapshot ref approach
                    await clickByRef(p, args.ref);
                  });
              }
            } else {
              // No node in refMap, try generic approach
              await clickByRef(p, args.ref);
            }
          } else if (args.selector) {
            const locator = await getLocatorForSelector(p, args.selector);
            await locator.click({ timeout: 6000 });
          }
          await clearAnnotation(p);
          await p.waitForTimeout(20); // Further reduced from 100ms to 20ms
          return `Clicked successfully`;
        } catch (e) {
          return `Click failed: ${e.message}`;
        }
      }

      case "browser_drag": {
        try {
          await highlightDragTarget(p, args);
          const targetRect = await getTargetBoundingBox(p, args);
          if (!targetRect) {
            throw new Error("Could not determine drag target bounds");
          }

          const {
            startX,
            startY,
            endX,
            endY,
          } = resolveDragCoordinates(targetRect, args);

          await p.mouse.move(startX, startY);
          await p.mouse.down();
          await p.mouse.move(endX, endY, { steps: 12 });
          await p.mouse.up();
          await clearAnnotation(p);
          await p.waitForTimeout(100);
          return `Dragged from (${Math.round(startX)}, ${Math.round(startY)}) to (${Math.round(endX)}, ${Math.round(endY)})`;
        } catch (e) {
          return `Drag failed: ${e.message}`;
        }
      }

      case "browser_fill": {
        try {
          if (args.ref) {
            const node = _refMap.get(args.ref);
            // Check if this is a contenteditable element (CodeMirror editor)
            if (isContentEditableNode(node)) {
              // For contenteditable elements, we need a different approach
              // First try to click on it to focus
              await p
                .locator('[contenteditable="true"]')
                .first()
                .click({ timeout: 3000 })
                .catch(() => {
                  throw new Error("Could not focus contenteditable element");
                });

              // Special handling for CodeMirror editors
              // Try multiple approaches to clear content
              try {
                // Approach 1: Use JavaScript to select all and delete
                await p.evaluate(() => {
                  const selection = window.getSelection();
                  const range = document.createRange();
                  const editable = document.querySelector(
                    '[contenteditable="true"]',
                  );
                  if (editable) {
                    range.selectNodeContents(editable);
                    selection.removeAllRanges();
                    selection.addRange(range);
                  }
                });
                await p.keyboard.press("Delete");
              } catch (jsError) {
                // Approach 2: Try Ctrl+A multiple times (sometimes needed for CodeMirror)
                await p.keyboard.down("Control");
                await p.keyboard.press("a");
                await p.keyboard.press("a"); // Second Ctrl+A might select all in CodeMirror
                await p.keyboard.up("Control");
                await p.keyboard.press("Delete");
              }

              // Type the new content - faster typing
              await p.keyboard.type(args.value, { delay: 10 }); // Reduced from 40ms to 10ms
              return `Typed into contenteditable element: "${args.value.slice(0, 50)}..."`;
            } else {
              // Regular input/textarea resolved from the snapshot ref
              await fillByRef(p, args.ref, args.value);
              return `Filled with "${args.value}"`;
            }
          } else if (args.selector) {
            await p.waitForSelector(args.selector, { timeout: 6000 });
            await p.fill(args.selector, args.value);
            return `Filled with "${args.value}"`;
          } else {
            return `Fill failed: need either ref or selector`;
          }
        } catch (e) {
          // Try click + type as fallback
          try {
            if (args.selector) {
              await p.click(args.selector);
              await p.keyboard.type(args.value, { delay: 40 });
              return `Typed "${args.value}"`;
            } else if (args.ref) {
              // Try to click by ref first, then type
              await this.executeTool("browser_click", { ref: args.ref });
              await p.keyboard.type(args.value, { delay: 40 });
              return `Typed "${args.value}"`;
            }
          } catch {}
          return `Fill failed: ${e.message}`;
        }
      }

      case "browser_select": {
        try {
          if (args.selector) {
            await p
              .selectOption(args.selector, { label: args.value })
              .catch(() => p.selectOption(args.selector, { value: args.value }));
          } else if (args.ref) {
            await selectByRef(p, args.ref, args.value);
          } else {
            throw new Error("need either ref or selector");
          }
          return `Selected "${args.value}"`;
        } catch (e) {
          return `Select failed: ${e.message}`;
        }
      }

      case "browser_hover": {
        try {
          if (args.selector) {
            await p.hover(args.selector, { timeout: 5000 });
          } else if (args.ref) {
            await hoverByRef(p, args.ref);
          } else {
            throw new Error("need either ref or selector");
          }
          await p.waitForTimeout(100); // Reduced from 500ms to 100ms
          return `Hovering over element`;
        } catch (e) {
          return `Hover failed: ${e.message}`;
        }
      }

      case "browser_scroll": {
        if (args.selector) {
          try {
            await p.$eval(
              args.selector,
              (el, { dir, amt }) => el.scrollBy(0, dir === "down" ? amt : -amt),
              { dir: args.direction, amt: args.amount },
            );
          } catch {
            await p.mouse.wheel(
              0,
              args.direction === "down" ? args.amount : -args.amount,
            );
          }
        } else {
          await p.mouse.wheel(
            0,
            args.direction === "down" ? args.amount : -args.amount,
          );
        }
        await p.waitForTimeout(100); // Reduced from 300ms to 100ms
        return `Scrolled ${args.direction} ${args.amount}px`;
      }

      case "browser_wait": {
        const waitMs = Math.max(0, Number(args.ms) || 0);
        await p.waitForTimeout(waitMs);
        return `Waited ${waitMs}ms`;
      }

      case "browser_press_key": {
        // Handle special key combinations
        const key = args.key;
        if (
          key.toLowerCase() === "ctrl+a" ||
          key === "Control+A" ||
          key === "Control+a"
        ) {
          await p.keyboard.press("Control+A");
        } else if (
          key.toLowerCase() === "ctrl+enter" ||
          key === "Control+Enter"
        ) {
          await p.keyboard.press("Control+Enter");
        } else if (key.includes("+")) {
          // Handle other key combinations
          await p.keyboard.press(key);
        } else {
          // Single key
          await p.keyboard.press(key);
        }
        await p.waitForTimeout(100); // Reduced from 300ms to 100ms
        return `Pressed ${args.key}`;
      }

      case "inject_annotation": {
        // Track first action time if not already set
        if (!this.firstActionTime) {
          this.firstActionTime = Date.now();
        }
        await injectAnnotation(
          p,
          args.description,
          await resolveAnnotationTarget(p, args),
        );
        return `Annotation shown: "${args.description}"`;
      }

      case "clear_annotation": {
        await clearAnnotation(p);
        return `Annotation cleared`;
      }

      case "finish_recording": {
        return `__DONE__: ${args.summary}`;
      }

      case "show_end_annotation": {
        await showEndAnnotation(p);
        await p.waitForTimeout(3000);
        return `Showed "The End" annotation with confetti`;
      }

      default:
        return `Unknown tool: ${name}`;
    }
  }

  async close() {
    if (this.page) {
      const videoPath = await this.page.video()?.path();
      this.webmPath = videoPath || null;
    }
    await this.context?.close();
    await this.browser?.close();

    // Calculate trim start time if we have both timestamps
    let trimStartSeconds = 0;
    if (this.recordingStartTime && this.firstActionTime) {
      const timeDiffMs = this.firstActionTime - this.recordingStartTime;
      trimStartSeconds = Math.max(0, timeDiffMs / 1000 - 0.5); // Start 0.5 seconds before first action
    }

    // Add final segment if we're tracking delays
    if (this.cutDelays && this.currentSegmentStart !== null) {
      this.addSegment()
    }

    return {
      webmPath: this.webmPath,
      trimStartSeconds: trimStartSeconds,
      actionSegments: this.actionSegments,
      recordingStartTime: this.recordingStartTime,
    };
  }

  /**
   * Enable delay cutting between actions
   */
  enableDelayCutting() {
    this.cutDelays = true;
  }
}

// ── Helpers ───────────────────────────────────────────────────────────────────

let _refCounter = 0;
const _refMap = new Map();

function formatSnapshot(node, depth, refStart) {
  let text = "";
  let refCount = refStart;

  const indent = "  ".repeat(depth);
  const ref = `e${refCount++}`;

  const role = node.role || "generic";
  const name = node.name ? ` "${node.name}"` : "";
  const value = node.value ? ` value="${node.value}"` : "";
  const checked = node.checked !== undefined ? ` checked=${node.checked}` : "";
  const disabled = node.disabled ? " disabled" : "";
  const focused = node.focused ? " focused" : "";

  // Only include interactive or named elements
  const interesting =
    [
      "button",
      "link",
      "textbox",
      "checkbox",
      "radio",
      "combobox",
      "menuitem",
      "tab",
      "heading",
      "listitem",
      "img",
      "search",
    ].includes(role) || node.name;

  if (interesting || depth === 0) {
    text += `${indent}[${ref}] ${role}${name}${value}${checked}${disabled}${focused}\n`;
    _refMap.set(ref, node);
  }

  if (node.children) {
    for (const child of node.children) {
      const result = formatSnapshot(child, depth + 1, refCount);
      text += result.text;
      refCount = result.refCount;
    }
  }

  return { text, refCount };
}

async function clickByRef(page, ref) {
  const locator = await getVisibleLocatorByRef(page, ref);
  await locator.click({ timeout: 5000 });
}

async function fillByRef(page, ref, value) {
  const locator = await getVisibleLocatorByRef(page, ref);
  await locator.fill(value, { timeout: 5000 });
}

async function selectByRef(page, ref, value) {
  const locator = await getVisibleLocatorByRef(page, ref);
  await locator
    .selectOption({ label: value })
    .catch(() => locator.selectOption({ value }));
}

async function hoverByRef(page, ref) {
  const locator = await getVisibleLocatorByRef(page, ref);
  await locator.hover({ timeout: 5000 });
}

function getLocatorByRef(page, ref) {
  const node = _refMap.get(ref);
  if (!node) throw new Error(`No element found for ref ${ref}`);

  const normalizedRole = normalizeRole(node.role);

  if (normalizedRole === "canvas") {
    return page.locator("canvas").first();
  }

  if (node.name) {
    return page
      .getByRole(normalizedRole, { name: node.name, exact: true })
      .first();
  }

  if (normalizedRole && normalizedRole !== "generic") {
    return page.getByRole(normalizedRole).first();
  }

  throw new Error(`Element ref ${ref} does not have enough metadata to locate it`);
}

function isContentEditableNode(node) {
  return Boolean(
    node &&
      (node.contenteditable === true ||
        node.contenteditable === "true" ||
        node.multiline === true ||
        node.multiline === "true"),
  );
}

function extractRefFromSelector(selector) {
  const match =
    typeof selector === "string"
      ? selector.match(/\[ref=["']?([^"'\\\]]+)["']?\]/)
      : null;
  return match ? match[1] : null;
}

async function resolveAnnotationTarget(page, args) {
  if (args.ref) {
    return { targetRect: await getBoundingBoxByRef(page, args.ref) };
  }

  const refFromSelector = extractRefFromSelector(args.selector);
  if (refFromSelector) {
    return { targetRect: await getBoundingBoxByRef(page, refFromSelector) };
  }

  return { selector: args.selector || null };
}

async function getTargetBoundingBox(page, args) {
  if (args.ref) {
    return await getBoundingBoxByRef(page, args.ref);
  }

  if (args.selector) {
    return await getBoundingBoxBySelector(page, args.selector);
  }

  throw new Error("need either ref or selector");
}

async function getBoundingBoxByRef(page, ref) {
  const locator = await getVisibleLocatorByRef(page, ref);
  return await locator.boundingBox();
}

async function getBoundingBoxBySelector(page, selector) {
  const locator = await getLocatorForSelector(page, selector);
  return await locator.boundingBox();
}

async function highlightClickTarget(page, args) {
  const target = await resolveAnnotationTarget(page, args);
  if (!target.selector && !target.targetRect) return;

  await injectAnnotation(page, 'Clicking here', target);
  await page.waitForTimeout(250);
}

async function highlightDragTarget(page, args) {
  const target = await resolveAnnotationTarget(page, args);
  if (!target.selector && !target.targetRect) return;

  await injectAnnotation(page, "Dragging here", target);
  await page.waitForTimeout(250);
}

async function getVisibleLocatorByRef(page, ref) {
  const locator = getLocatorByRef(page, ref);
  await ensureLocatorVisible(locator);
  return locator;
}

async function getLocatorForSelector(page, selector) {
  await page.waitForSelector(selector, { timeout: 6000 });
  const locator = page.locator(selector).first();
  await ensureLocatorVisible(locator);
  return locator;
}

async function ensureLocatorVisible(locator) {
  if (typeof locator.scrollIntoViewIfNeeded === "function") {
    await locator.scrollIntoViewIfNeeded();
  }
}

function parseSnapshotForRefMap(snapshotString) {
  // Clear the existing ref map
  _refMap.clear();

  const lines = snapshotString.split("\n");

  for (const line of lines) {
    // Extract ref if present
    const refMatch = line.match(/\[ref=([^\]]+)\]/);
    if (!refMatch) continue;

    const ref = refMatch[1];

    // Extract role (first word after bullet)
    const roleMatch = line.match(/^\s*-\s+(\w+)/);
    const role = normalizeRole(roleMatch ? roleMatch[1] : "generic");

    // Extract name if present (text in quotes)
    const nameMatch = line.match(/"([^"]+)"/);
    const name = nameMatch ? nameMatch[1] : null;

    // Extract other attributes
    const node = { role, name };
    const attrMatches = line.matchAll(/\[([^=\]]+)(?:=([^\]]+))?\]/g);
    for (const match of attrMatches) {
      const key = match[1];
      const value = match[2] || true;
      if (key !== "ref") {
        node[key] = value;
      }
    }

    _refMap.set(ref, node);
  }
}

function normalizeRole(role) {
  return typeof role === "string" ? role.toLowerCase() : "generic";
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

function resolveDragCoordinates(targetRect, args) {
  const maxX = Math.max(1, targetRect.width - 1);
  const maxY = Math.max(1, targetRect.height - 1);

  const defaultStartX = Math.max(12, Math.round(targetRect.width * 0.2));
  const defaultStartY = Math.max(12, Math.round(targetRect.height * 0.2));
  const defaultEndX = Math.max(defaultStartX + 24, Math.round(targetRect.width * 0.7));
  const defaultEndY = Math.max(defaultStartY + 24, Math.round(targetRect.height * 0.45));

  const startOffsetX = clamp(Number(args.startX ?? defaultStartX), 1, maxX);
  const startOffsetY = clamp(Number(args.startY ?? defaultStartY), 1, maxY);
  const endOffsetX = clamp(Number(args.endX ?? defaultEndX), 1, maxX);
  const endOffsetY = clamp(Number(args.endY ?? defaultEndY), 1, maxY);

  return {
    startX: targetRect.x + startOffsetX,
    startY: targetRect.y + startOffsetY,
    endX: targetRect.x + endOffsetX,
    endY: targetRect.y + endOffsetY,
  };
}

export const __testables = {
  clamp,
  extractRefFromSelector,
  getLocatorByRef,
  getLocatorForSelector,
  getTargetBoundingBox,
  getVisibleLocatorByRef,
  highlightDragTarget,
  isContentEditableNode,
  parseSnapshotForRefMap,
  highlightClickTarget,
  normalizeRole,
  ensureLocatorVisible,
  resolveDragCoordinates,
  resolveAnnotationTarget,
};
