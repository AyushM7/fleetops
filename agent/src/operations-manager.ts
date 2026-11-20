import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import type { MastraModelConfig } from "@mastra/core/llm";
import { z } from "zod";
import { resolveEvaluationModel } from "./evaluation-investigation-model.js";

import { operationsModelId, operationsOutputLimit } from "./operations-fetch.js";

const jobId = z.string().regex(/^J[1-9][0-9]{0,18}$/);
const transportId = z.string().regex(/^R([1-9]|10)$/);
const decisionSchema = z.object({
  decision: z.discriminatedUnion("kind", [
    z.strictObject({
      kind: z.literal("abstain"),
      reason: z.enum(["no_safe_option", "prefer_existing_dispatch", "insufficient_headroom"]),
    }),
    z.strictObject({
      kind: z.literal("assign"),
      assignments: z.array(z.strictObject({ jobId, robotId: transportId })).min(1).max(10),
    }),
    z.strictObject({
      kind: z.literal("reassign"),
      reassignment: z.strictObject({ jobId, fromRobotId: transportId, toRobotId: transportId }),
    }),
  ]),
}).strict();

const instructions = [
  "You manage transport jobs in the FleetOps warehouse simulation.",
  "A Go observation supplies robot roles, battery, job custody, charging reservations, aisle occupancy and deadlocks.",
  "The assignments and reassignments lists are snapshot-scoped Go route and energy previews, never reservations.",
  "Use only pairs listed in those previews. A batch needs distinct jobs and distinct robots.",
  "Favor feasible urgent PRIORITY deadlines while giving overdue and older NORMAL work a chance.",
  "Prefer short routes, spare battery after delivery and charging, and fewer traffic conflicts.",
  "Reassign only an explicitly listed uncollected job when it improves progress. Never interrupt carrying cargo.",
  "If the case is unclear or none of the commands would help, abstain using one of the allowed reasons.",
  "Return only the structured decision.",
].join(" ");

export type OperationsModelResult = {
  decision?: z.infer<typeof decisionSchema>["decision"];
  failure?: "invalid_output" | "model_unavailable" | "billing_anomaly";
  usage?: { model: string; inputTokens: number; outputTokens: number };
};

export async function makeOperationsDecision(observation: string, model: MastraModelConfig,
  abortSignal: AbortSignal, expectedModel?: string): Promise<OperationsModelResult> {
  if (Buffer.byteLength(observation) > 48 * 1024 || abortSignal.aborted) return { failure: "model_unavailable" };
  const agent = new Agent({
    id: "fleetops-operations", name: "FleetOps Operations Manager", instructions,
    model, maxRetries: 0, errorProcessorDefaults: false,
  });
  agent.__setLogger(noopLogger);
  try {
    let reportedModel: string | undefined;
    let knownUnexpectedBilling = false;
    const response = await agent.generate(observation, {
      maxSteps: 1, activeTools: [], toolChoice: "none", abortSignal,
      modelSettings: { maxOutputTokens: operationsOutputLimit, maxRetries: 0 },
      providerOptions: { openai: { store: false, serviceTier: "default", reasoningEffort: "none", strictJsonSchema: true } },
      structuredOutput: { schema: decisionSchema, errorStrategy: "warn" },
      onStepFinish: step => {
        reportedModel = step.response.modelId;
        const metadata = step.providerMetadata?.openai;
        const tier = metadata?.serviceTier;
        const details = metadata?.usage;
        knownUnexpectedBilling = (reportedModel !== undefined && reportedModel !== expectedModel) ||
          (typeof tier === "string" && tier !== "default" && tier !== "standard") ||
          (typeof details === "object" && details !== null &&
            Object.entries(details).some(([key, value]) => key.startsWith("orchestration") && value !== 0));
      },
    });
    if (expectedModel && knownUnexpectedBilling) return { failure: "billing_anomaly" };
    const { inputTokens, outputTokens, totalTokens } = response.totalUsage;
    const usage = reportedModel === operationsModelId && inputTokens !== undefined && outputTokens !== undefined &&
      Number.isSafeInteger(inputTokens) && inputTokens > 0 && Number.isSafeInteger(outputTokens) &&
      outputTokens >= 0 && inputTokens + outputTokens === totalTokens ? { model: reportedModel, inputTokens, outputTokens } : undefined;
    const parsed = decisionSchema.safeParse(response.object);
    return parsed.success && response.finishReason === "stop" ? { decision: parsed.data.decision, usage } : { failure: "invalid_output", usage };
  } catch {
    // SDK errors and raw model output can contain secrets and private reasoning.
    return { failure: "model_unavailable" };
  }
}

// The host's permit is checked at the bounded provider invocation.
export async function makeOpenAIOperationsDecision(observation: string, permit: () => void,
  signal: AbortSignal): Promise<OperationsModelResult> {
  try {
    const model = await resolveEvaluationModel(operationsModelId, operationsOutputLimit, permit);
    return await makeOperationsDecision(observation, model, signal, operationsModelId);
  } catch {
    return { failure: "model_unavailable" };
  }
}
