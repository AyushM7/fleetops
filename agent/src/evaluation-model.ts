import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import type { MastraModelConfig } from "@mastra/core/llm";
import type { FleetState } from "./contracts.js";
import { reasoningSchema } from "./contracts.js";
import { instructions, reasoningPrompt } from "./agent.js";

export interface GenerationSettings { maxOutputTokens: number; temperature?: number }
export interface CapturedResponse { text: string | null; callFailed: boolean }

// No FleetAPI, tools, memory, submission or simulator capabilities enter this path.
export async function generateEvaluationResponse(state: FleetState, model: MastraModelConfig,
  settings: GenerationSettings): Promise<CapturedResponse> {
  const agent = new Agent({ id: "fleetops-evaluation", name: "FleetOps evaluation", instructions,
    model, maxRetries: 0 });
  agent.__setLogger(noopLogger);
  let text: string | null = null;
  try {
    const response = await agent.generate(reasoningPrompt(state), {
      maxSteps: 1, activeTools: [], toolChoice: "none", modelSettings: settings,
      abortSignal: AbortSignal.timeout(30_000),
      // Preserve invalid output for independent scoring rather than replacing it.
      structuredOutput: { schema: reasoningSchema, errorStrategy: "warn" },
      onStepFinish: step => { text = step.text; },
    });
    return { text: response.text, callFailed: false };
  } catch {
    // Provider errors may contain credentials or request headers. Never serialize them.
    return { text, callFailed: true };
  }
}
