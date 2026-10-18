import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import type { LanguageModel } from "@mastra/core/llm";

export type EvaluationModel = Extract<LanguageModel, { specificationVersion: "v2" }>;
export type EvaluationRequest = Parameters<EvaluationModel["doGenerate"]>[0];
export function capEvaluationModel(model: EvaluationModel, maximumOutputTokens: number, permit: () => void): EvaluationModel {
  if (!Number.isSafeInteger(maximumOutputTokens) || maximumOutputTokens < 1 || maximumOutputTokens > 6000) throw new Error("Invalid reviewed output cap.");
  const call = (options: EvaluationRequest) => {
    permit();
    return model.doGenerate({ ...options, maxOutputTokens: Math.min(options.maxOutputTokens ?? maximumOutputTokens, maximumOutputTokens),
      providerOptions: { ...options.providerOptions, openai: { ...options.providerOptions?.openai, store: false, serviceTier: "default" } } });
  };
  return new Proxy(model, { get(target, property) {
    if (property === "doGenerate" || property === "doStream") return call;
    const value: unknown = Reflect.get(target, property, target);
    return typeof value === "function" ? value.bind(target) : value;
  } });
}
// Only the separately gated local evaluation CLI uses this resolver. It never generates.
export async function resolveEvaluationModel(modelId: string, maximumOutputTokens: number, permit: () => void): Promise<EvaluationModel> {
  const agent = new Agent({ id: "agent07-reviewed-resolver", name: "Reviewed evaluation resolver", instructions: "Resolve the exact reviewed model.",
    model: { providerId: "openai", modelId, url: "https://api.openai.com/v1", api: "responses" }, maxRetries: 0, errorProcessorDefaults: false });
  agent.__setLogger(noopLogger);
  const resolved = await agent.getModel();
  if (resolved.specificationVersion !== "v2") throw new Error("Unsupported reviewed model protocol.");
  return capEvaluationModel(resolved, maximumOutputTokens, permit);
}
