#!/usr/bin/env node
import readline from 'readline/promises';
import { stdin as input, stdout as output } from 'process';
import path from 'path';
import fs from 'fs/promises';
import { Browser } from './browser.js';
import { Agent } from './agent.js';
import { convertToMp4 } from './converter.js';

// ── Check API key ─────────────────────────────────────────────────────────────
if (!process.env.DEEPSEEK_API_KEY) {
  console.error('\n❌  DEEPSEEK_API_KEY is not set.');
  console.error('    Set it in .env or export it first: export DEEPSEEK_API_KEY=sk-...\n');
  process.exit(1);
}

// ── Parse command line arguments ──────────────────────────────────────────────
function parseArgs() {
  const args = process.argv.slice(2);
  const result = { file: null, cutDelays: false };
  
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '-f' || args[i] === '--file') {
      if (i + 1 < args.length && !args[i + 1].startsWith('-')) {
        result.file = args[i + 1];
        i++;
      } else {
        console.error('  ❌  -f flag requires a file path');
        process.exit(1);
      }
    } else if (args[i].startsWith('--file=')) {
      result.file = args[i].substring(7);
    } else if (args[i] === '--cut-delays') {
      result.cutDelays = true;
    }
  }
  return result;
}

// ── Extract URL from markdown bullet ──────────────────────────────────────────
function extractUrlFromMarkdown(content) {
  // Look for first line with backticks containing http:// or https://
  const lines = content.split('\n');
  for (const line of lines) {
    const match = line.match(/`(https?:\/\/[^`]+)`/);
    if (match) {
      return match[1].trim();
    }
  }
  return null;
}

// ── Read scenario from file ───────────────────────────────────────────────────
async function readScenarioFile(filePath) {
  try {
    const content = await fs.readFile(filePath, 'utf-8');
    const lines = content.split('\n');
    
    // Try to extract URL from markdown first
    let url = extractUrlFromMarkdown(content);
    
    if (!url) {
      // Fallback: first line as URL (simple format)
      url = lines[0].trim();
      if (!url.startsWith('http://') && !url.startsWith('https://')) {
        throw new Error('First line must be a valid URL starting with http:// or https://');
      }
    }
    
    // Scenario is the entire content (for markdown) or lines 1+ (for simple format)
    let scenario;
    if (extractUrlFromMarkdown(content)) {
      // For markdown, use entire content as scenario
      scenario = content.trim();
    } else {
      // For simple format, use lines 1+
      scenario = lines.slice(1).join('\n').trim();
    }
    
    if (!scenario) {
      throw new Error('Scenario cannot be empty');
    }
    
    return { url, scenario };
  } catch (err) {
    console.error(`  ❌  Error reading file "${filePath}":`, err.message);
    process.exit(1);
  }
}

// ── Banner ────────────────────────────────────────────────────────────────────
console.log(`
  ████████╗ █████╗ ██╗  ██╗███████╗███████╗
     ██╔══╝██╔══██╗██║ ██╔╝██╔════╝██╔════╝
     ██║   ███████║█████╔╝ █████╗  ███████╗
     ██║   ██╔══██║██╔═██╗ ██╔══╝  ╚════██║
     ██║   ██║  ██║██║  ██╗███████╗███████║
     ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝╚══════╝
  AI demo recorder — powered by DeepSeek + Playwright
`);

// ── Get URL and scenario ──────────────────────────────────────────────────────
const args = parseArgs();
let url, scenario;

if (args.file) {
  console.log(`  📁  Reading scenario from: ${args.file}`);
  const fileData = await readScenarioFile(args.file);
  url = fileData.url;
  scenario = fileData.scenario;
  console.log(`  🔗  URL: ${url}`);
  console.log(`  📝  Scenario loaded from file\n`);
} else {
  // Interactive mode
  const rl = readline.createInterface({ input, output });

  url = (await rl.question('  App URL: ')).trim();
  if (!url || !url.startsWith('http')) {
    console.error('  ❌  Please enter a valid URL starting with http:// or https://');
    process.exit(1);
  }

  console.log('\n  Describe the demo scenario.');
  console.log('  Example: "Show how a user signs up, creates a project, and invites a teammate"\n');
  scenario = (await rl.question('  Scenario: ')).trim();
  if (!scenario) {
    console.error('  ❌  Scenario cannot be empty.');
    process.exit(1);
  }

  rl.close();
}

// ── Setup output dir ──────────────────────────────────────────────────────────
const outputDir = path.resolve('./take5-output');
await fs.mkdir(outputDir, { recursive: true });

console.log(`\n  Output → ${outputDir}`);
if (args.cutDelays) {
  console.log('  ⚡ Delay cutting enabled (will remove pauses between actions)');
}
console.log('  Starting browser and agent...\n');
console.log('─'.repeat(60));

// ── Run ───────────────────────────────────────────────────────────────────────
const browser = new Browser();

// Enable delay cutting if requested
if (args.cutDelays) {
  browser.enableDelayCutting();
}

let closeResult = null;

try {
  await browser.launch({ outputDir });

  const agent = new Agent(browser);
  await agent.run(url, scenario);

} catch (err) {
  console.error('\n❌  Agent error:', err.message);
  if (process.env.DEBUG) console.error(err.stack);
} finally {
  console.log('\n⏹  Closing browser...');
  closeResult = await browser.close();
}

// ── Convert ───────────────────────────────────────────────────────────────────
if (closeResult && closeResult.webmPath) {
  const { webmPath, trimStartSeconds, actionSegments, recordingStartTime } = closeResult;
  console.log(`💾  Raw video: ${webmPath}`);
  
  if (args.cutDelays && actionSegments && actionSegments.length > 0) {
    console.log(`✂️  Detected ${actionSegments.length} action segments for delay cutting`);
    const totalDuration = actionSegments.reduce((sum, seg) => sum + (seg.end - seg.start), 0);
    console.log(`   Total action time: ${(totalDuration / 1000).toFixed(1)}s`);
  } else if (trimStartSeconds > 0) {
    console.log(`⏱️  First action started at: ${trimStartSeconds.toFixed(1)}s (will trim beginning)`);
  }
  
  try {
    const mp4 = await convertToMp4(webmPath, trimStartSeconds, actionSegments, recordingStartTime);
    if (mp4) {
      console.log(`\n🎉  Done! Your demo is ready:`);
      console.log(`    ${path.resolve(mp4)}\n`);
    } else {
      console.log(`\n🎉  Done! Raw video saved:`);
      console.log(`    ${path.resolve(webmPath)}\n`);
    }
  } catch {
    console.log(`\n🎉  Done! Raw video saved:`);
    console.log(`    ${path.resolve(webmPath)}\n`);
  }
} else {
  console.log('\n⚠️  No video file found. The recording may not have started correctly.');
}
