import OpenAI from 'openai';
import { SYSTEM_PROMPT } from './prompt.js';

export class Agent {
  constructor(browser) {
    this.browser = browser;
    this.messages = [];
    this.client = new OpenAI({
      apiKey: process.env.DEEPSEEK_API_KEY,
      baseURL: 'https://api.deepseek.com',
    });
  }

  async run(url, scenario) {
    const tools = this.browser.getToolDefinitions();

    // Seed the conversation
    this.messages = [
      {
        role: 'user',
        content: `App URL: ${url}\n\nScenario to demonstrate:\n${scenario}\n\nStart by navigating to the app and taking a snapshot to understand the current state. Then execute the scenario.`,
      },
    ];

    console.log('\n🤖 Agent starting...\n');

    let iteration = 0;
    const MAX_ITERATIONS = 60;

    while (iteration < MAX_ITERATIONS) {
      iteration++;

      // Call DeepSeek
      const response = await this.client.chat.completions.create({
        model: 'deepseek-chat',
        messages: [{ role: 'system', content: SYSTEM_PROMPT }, ...this.messages],
        tools,
        tool_choice: 'auto',
        temperature: 0.3,
        max_tokens: 2048,
      });

      const msg = response.choices[0].message;
      this.messages.push(msg);

      // Print agent reasoning
      if (msg.content) {
        console.log(`\n💭 ${msg.content}`);
      }

      // No tool calls — agent is done or stuck
      if (!msg.tool_calls || msg.tool_calls.length === 0) {
        console.log('\n⚠️  Agent stopped calling tools unexpectedly.');
        break;
      }

      // Execute each tool call
      const toolResults = [];
      let finished = false;
      let summary = '';

      for (const call of msg.tool_calls) {
        const name = call.function.name;
        let args = {};
        try {
          args = JSON.parse(call.function.arguments || '{}');
        } catch {
          args = {};
        }

        console.log(`  🔧 ${name}(${formatArgs(args)})`);

        const result = await this.browser.executeTool(name, args);
        console.log(`     → ${String(result).slice(0, 120)}`);

        if (String(result).startsWith('__DONE__')) {
          finished = true;
          summary = String(result).replace('__DONE__: ', '');
        }

        toolResults.push({
          role: 'tool',
          tool_call_id: call.id,
          content: String(result),
        });
      }

      // Add tool results to history
      this.messages.push(...toolResults);

      if (finished) {
        console.log(`\n✅ Agent finished: ${summary}`);
        return summary;
      }
    }

    if (iteration >= MAX_ITERATIONS) {
      console.warn('\n⚠️  Max iterations reached — stopping agent.');
    }

    return 'Demo recording completed';
  }
}

function formatArgs(args) {
  const entries = Object.entries(args);
  if (entries.length === 0) return '';
  return entries
    .map(([k, v]) => `${k}=${JSON.stringify(String(v).slice(0, 40))}`)
    .join(', ');
}
