import { Agent } from "@mastra/core/agent";
import { expect, it, vi } from "vitest";
import { InvestigationBudget, investigationLimits } from "./investigation-budget.js";
import { offlineModelFixture, pendingFixture, recoveryDecisionState } from "./fixtures.js";
import { runRecoveryDecision } from "./decision-agent.js";

type Model = Extract<Awaited<ReturnType<Agent["getModel"]>>, { specificationVersion: "v2" }>;
const request: Parameters<Model["doGenerate"]>[0] = { prompt: [{ role: "user", content: [{ type: "text", text: "Read evidence" }] }] };
function streamModel(usage: { inputTokens?: number; outputTokens?: number; totalTokens?: number }, content = true) {
  const model: Model = { specificationVersion: "v2", provider: "offline-budget", modelId: "budget", supportedUrls: {},
    doGenerate: vi.fn(async () => ({ stream: new ReadableStream({ start(controller) {
      if (content) controller.enqueue({ type: "finish", finishReason: "stop", usage });
      controller.close();
    } }) })), doStream: vi.fn(() => { throw new Error("Unexpected stream call"); }) };
  return model;
}
async function consume(model: Model, input = request): Promise<void> {
  const response = await model.doGenerate(input);
  for await (const _part of response.stream) { /* Consume usage guards as Mastra does. */ }
}
it("hard-caps actual provider invocations across repeated, concurrent and hidden extra calls", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 1, outputTokens: 1, totalTokens: 2 });
  try {
    const model = budget.model(provider, () => {});
    for (let i = 0; i < 6; i++) await consume(model);
    await expect(consume(model)).rejects.toThrow();
    expect(provider.doGenerate).toHaveBeenCalledTimes(6);
    await Promise.allSettled(Array.from({ length: 10 }, () => consume(model)));
    expect(provider.doGenerate).toHaveBeenCalledTimes(6);
  } finally { budget.close(); }
});
it("rejects concurrent model calls before they can reserve the same usage allowance", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 1, outputTokens: 1, totalTokens: 2 });
  try {
    const model = budget.model(provider, () => {});
    await Promise.allSettled(Array.from({ length: 10 }, () => consume(model)));
    expect(provider.doGenerate).toHaveBeenCalledTimes(1); expect(() => budget.check()).toThrow();
  } finally { budget.close(); }
});
it.each([
  { inputTokens: 32_001, outputTokens: 1, totalTokens: 32_002 },
  { inputTokens: 1, outputTokens: 6001, totalTokens: 6002 },
  { inputTokens: 1, outputTokens: 1, totalTokens: 44_001 },
  { inputTokens: undefined, outputTokens: 1, totalTokens: 2 },
  { inputTokens: 1, outputTokens: -1, totalTokens: 0 },
  { inputTokens: 10, outputTokens: 10, totalTokens: 2 },
])("fails closed for invalid or over-budget reported usage: %j", async usage => {
  const provider = streamModel(usage), budget = new InvestigationBudget();
  try { await expect(consume(budget.model(provider, () => {}))).rejects.toThrow(); expect(() => budget.check()).toThrow(); }
  finally { budget.close(); }
});
it("counts aggregate output and clamps the final allowance to remaining tokens", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 100, outputTokens: 4500, totalTokens: 4600 });
  try {
    const model = budget.model(provider, () => {});
    await consume(model); await consume(model);
    await expect(consume(model)).rejects.toThrow();
    expect(provider.doGenerate).toHaveBeenLastCalledWith(expect.objectContaining({ maxOutputTokens: 3000 }));
    expect(budget.trace.outputTokens).toBe(9000);
  } finally { budget.close(); }
});
it("rejects oversized inputs before the provider is invoked", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 1, outputTokens: 1, totalTokens: 2 });
  try {
    await expect(consume(budget.model(provider, () => {}), { prompt: [{ role: "user", content: [{ type: "text", text: "x".repeat(32_001) }] }] })).rejects.toThrow();
    expect(provider.doGenerate).not.toHaveBeenCalled();
  } finally { budget.close(); }
});
it("rejects missing usage/finish metadata", async () => {
  const budget = new InvestigationBudget();
  try { await expect(consume(budget.model(streamModel({}, false), () => {}))).rejects.toThrow(); }
  finally { budget.close(); }
});
it.each(["unknown usage", "excessive usage"])("never submits after %s from an actual Mastra model response", async mode => {
  const base = offlineModelFixture(null), model = { ...base, async doGenerate() {
    const result = await base.doGenerate();
    return { ...result, usage: mode === "unknown usage" ? { inputTokens: undefined, outputTokens: undefined, totalTokens: undefined }
      : { inputTokens: 33_000, outputTokens: 1, totalTokens: 33_001 } };
  } };
  const api = { readFleet: vi.fn().mockResolvedValue(recoveryDecisionState()), submitCharging: vi.fn().mockResolvedValue(pendingFixture),
    submitReassignment: vi.fn().mockResolvedValue(pendingFixture) };
  const result = await runRecoveryDecision(api, { enabled: true, model, objective: "compare_tradeoffs", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("failed"); expect(result.aiAnalysis.decision).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(investigationLimits.requests).toBe(6);
});
