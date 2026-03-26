# Take5 PRD

## Overview

Take5 is a CLI application that uses an LLM-driven browser agent to explore a live web app and record a polished product demo video. A user provides an application URL and a demo scenario, and Take5 opens a real Chromium browser, performs the scenario, visually highlights actions, records the session, and exports a video artifact.

The product is optimized for fast demo creation without pre-authored scripts, selectors, or hardcoded page knowledge.

## Problem

Creating product demo videos is slow and repetitive. Teams typically need to:

- decide what story to show
- manually operate the app in a browser
- re-record when flows break
- edit the resulting footage to remove dead time

This is especially painful for internal demos, sales enablement, release notes, and product walkthroughs where speed matters more than studio-grade production.

## Product Goal

Enable a user to generate a usable application demo video from a plain-language prompt and a URL, with minimal manual setup.

## Target Users

- Product managers creating walkthroughs
- Founders creating quick demos for prospects or investors
- Sales engineers preparing tailored product demos
- Developer advocates creating repeatable product showcases
- Internal teams documenting workflows after releases

## Primary Use Cases

- Record a demo of a web application for a specific workflow
- Show a feature flow in a browser with visible action callouts
- Generate a raw or polished recording artifact for sharing
- Create quick demos repeatedly without maintaining brittle selectors

## User Story

As a user, I want to describe what should be demonstrated in a web app so that the system can autonomously operate the browser and output a clean demo recording.

## Success Criteria

- A user can start the tool from the CLI with a URL and scenario
- The agent can navigate and interact with a live app using browser snapshots and tool calls
- Meaningful actions are visibly indicated in the recording
- The recording is saved reliably as `.mp4` when ffmpeg is available, otherwise as `.webm`
- Delay-heavy recordings can be shortened by trimming inactive sections

## Product Scope

### In Scope

- CLI-based experience
- Scenario input via prompt or file
- Chromium automation through Playwright
- LLM-guided exploration and task execution
- Visual action highlighting before interactions
- Browser video recording
- Optional conversion from `.webm` to `.mp4`
- Optional delay-cutting post-processing

### Out of Scope

- Multi-user collaboration
- Cloud-hosted execution
- Account system or dashboard
- Guaranteed deterministic replay of flows
- Native mobile app automation
- Rich timeline editing UI
- Audio narration or voiceover generation

## Core Product Flow

1. User launches Take5 from the command line.
2. User provides an app URL and a scenario, either interactively or through a file.
3. Take5 launches Chromium and starts recording.
4. The LLM agent navigates to the app and inspects the current page using a browser snapshot.
5. The agent performs actions such as click, fill, hover, select, scroll, and key presses.
6. Before important actions, the UI is temporarily highlighted to make the recording understandable.
7. The run ends when the agent signals completion or the session stops.
8. The recording is finalized and converted to `.mp4` if ffmpeg is available.

## Functional Requirements

### 1. CLI Input

- The system must support interactive input for URL and scenario.
- The system must support scenario input from a file via CLI flag.
- The system must validate that the URL is HTTP or HTTPS.
- The system must fail fast if the required API key is missing.

### 2. Browser Automation

- The system must launch a real Chromium browser window.
- The system must record browser video for the session.
- The system must support navigation, click, fill, hover, select, scroll, wait, and key press actions.
- The system must use browser snapshots to ground interactions in live page state.
- The system must scroll elements into view before visible click interactions when needed.

### 3. Agent Orchestration

- The system must send the user scenario and browser tools to the LLM.
- The system must support iterative tool-calling until completion or a maximum iteration count.
- The system must surface tool activity and agent reasoning in the terminal for debugging.
- The system must stop when the agent explicitly finishes or when safety limits are reached.

### 4. Recording UX

- The system must visually highlight click targets before click actions.
- The system should support descriptive annotations before other meaningful actions.
- The system should remove temporary annotations after actions complete.
- The system should support an explicit end-of-demo visual treatment.

### 5. Video Output

- The system must save the raw browser recording.
- The system must convert to `.mp4` when ffmpeg is available.
- The system should trim dead time at the beginning of the run.
- The system should support optional removal of idle gaps between meaningful actions.

### 6. Reliability

- The system must return readable errors for missing API keys, invalid inputs, and failed file reads.
- The system should gracefully fall back to `.webm` if ffmpeg is unavailable.
- The system should preserve a usable raw recording even if post-processing fails.

## Non-Functional Requirements

- Runs locally on macOS and Linux with Node.js 20.6+
- Uses real browser automation rather than a simulated DOM
- Keeps setup lightweight and local-first
- Provides enough terminal output for debugging failed runs
- Avoids requiring selector authoring by the user

## User Experience Requirements

- The workflow should feel simple enough for a first-time CLI user
- The recording should make it obvious what element is being interacted with
- The output should be understandable without watching mouse movement closely
- The terminal should explain what is happening during the run

## Inputs and Dependencies

### Required

- Node.js 20.6+
- DeepSeek-compatible API key in environment
- Playwright Chromium installation

### Optional

- ffmpeg for `.mp4` conversion

## Current Technical Design

- `src/cli.js`: entry point, argument parsing, input collection, orchestration
- `src/agent.js`: LLM loop and tool-call execution flow
- `src/browser.js`: Playwright wrapper and browser tool implementation
- `src/annotator.js`: in-page visual annotations and end-state visuals
- `src/converter.js`: `.webm` to `.mp4` conversion and delay cutting
- `src/prompt.js`: agent behavior instructions

## Risks

- Live applications may change structure mid-run and confuse the agent
- Authentication walls or anti-bot protections may block flows
- Long or ambiguous scenarios may produce inconsistent results
- Private or unstable browser APIs may create maintenance risk
- LLM-driven behavior can be non-deterministic across runs

## Metrics

Suggested product metrics:

- Demo completion rate
- Successful video export rate
- Average time from start to output file
- Average number of meaningful actions per run
- Rate of manual re-runs per scenario
- Percentage of sessions converted to `.mp4`

## Future Opportunities

- Named reusable scenarios
- Multi-provider LLM support
- Better login and credential handling
- Voiceover or subtitle generation
- Export presets for social, docs, and sales demos
- Browser state seeding and authenticated session reuse
- Web UI for job submission and video library management

## Release Readiness Checklist

- CLI input validated
- Browser actions grounded in snapshots
- Click targets highlighted before interaction
- Off-screen targets scrolled into view before clicks
- Recording artifact saved even when conversion fails
- Native tests cover core browser action behavior

## Example of script.md file
```
1. Navigate to app: `https://codev.bconf.com/#/app`
2. Select 'Two Sum' problem from the problem list
3. Find the code editor (CodeMirror component):
   - Look for elements with `[contenteditable="true"]` in the snapshot
   - Or elements with role="textbox" and name containing "code editor" or similar
   - The editor area should show the function stub: `function twoSum(nums, target) {}`
4. Write solution code:
   - First click on the code editor to focus it
   - **To clear existing content**: Use `browser_fill` tool - it automatically clears content before typing
   - **OR**: Use `browser_press_key(key="Control+a")` then `browser_press_key(key="Backspace")`
   - **IMPORTANT**: Type the ENTIRE solution code at once, not line by line
   - Use `browser_fill` with the complete code block (recommended - it handles clearing automatically)
   - Do NOT type line by line - this will not work correctly
5. Complete solution code to type (type this all at once):
   `
   function twoSum(nums, target) {
    const seen = new Map();
    for(let i=0;i<nums.length;i++){
        const diff = target - nums[i];
        if(seen.has(diff)){
        return [seen.get(diff),i];
        }
        seen.set(nums[i],i);
    }
    return [];
   }
   `
6. Run tests using the "Run tests" button
7. Scroll down to see all tests passed
8. Submit solution for review
9. Wait for review to appear
10. Scroll up on right panel to view the all review
11. Press the "Next problem" button to continue
12. At the very end, show "The End" annotation with confetti animation using `show_end_annotation()` tool
```