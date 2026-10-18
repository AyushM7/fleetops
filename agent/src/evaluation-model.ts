import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import type { MastraModelConfig } from "@mastra/core/llm";
import type { FleetState } from "./contracts.js";
import { reasoningSchema } from "./contracts.js";
import { instructions, reasoningPrompt } from "./agent.js";

export type PromptVariant = "assisted" | "reduced-hint";
export interface GenerationSettings { maxOutputTokens: number; temperature?: number; promptVariant?: PromptVariant }
export interface CapturedResponse { text: string | null; callFailed: boolean; latencyMs?: number;
  usage?: { inputTokens: number | null; outputTokens: number | null; totalTokens: number | null } }

export function evaluationPrompt(state: FleetState, variant: PromptVariant = "assisted"): string {
  const payload = JSON.parse(reasoningPrompt(state)) as Record<string, unknown>;
  if (variant === "reduced-hint") delete payload.supportedAction;
  return JSON.stringify(payload);
}

// No FleetAPI, tools, memory, submission or simulator capabilities enter this path.
export async function generateEvaluationResponse(state: FleetState, model: MastraModelConfig,
  settings: GenerationSettings): Promise<CapturedResponse> {
  // Stability processors can retry independently of the model/agent retry limit.
  const agent = new Agent({ id: "fleetops-evaluation", name: "FleetOps evaluation", instructions,
    model, maxRetries: 0, errorProcessorDefaults: false });
  agent.__setLogger(noopLogger);
  let text: string | null = null;
  const started = performance.now();
  try {
    const response = await agent.generate(evaluationPrompt(state, settings.promptVariant), {
      maxSteps: 1, activeTools: [], toolChoice: "none", modelSettings: { maxOutputTokens: settings.maxOutputTokens, temperature: settings.temperature, maxRetries: 0 },
      providerOptions: { openai: { store: false, serviceTier: "default" } },
      abortSignal: AbortSignal.timeout(30_000),
      // Preserve invalid output for independent scoring rather than replacing it.
      structuredOutput: { schema: reasoningSchema, errorStrategy: "warn" },
      onStepFinish: step => { text = step.text; },
    });
    const usage = response.totalUsage;
    const tokens = (n: unknown): number | null => typeof n === "number" && Number.isSafeInteger(n) && n >= 0 ? n : null;
    return { text: response.text, callFailed: false, latencyMs: Math.round(performance.now() - started),
      usage: { inputTokens: tokens(usage.inputTokens), outputTokens: tokens(usage.outputTokens), totalTokens: tokens(usage.totalTokens) } };
  } catch {
    // Provider errors may contain credentials or request headers. Never serialize them.
    return { text, callFailed: true, latencyMs: Math.round(performance.now() - started) };
  }
}
