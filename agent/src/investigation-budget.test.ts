import { Agent } from "@mastra/core/agent";
import { expect, it, vi } from "vitest";
import { InvestigationBudget, investigationLimits, normalizeInvestigationUsage } from "./investigation-budget.js";
import { offlineModelFixture, pendingFixture, recoveryDecisionState } from "./fixtures.js";
import { runRecoveryDecision } from "./decision-agent.js";

type Model = Extract<Awaited<ReturnType<Agent["getModel"]>>, { specificationVersion: "v2" }>;
const request: Parameters<Model["doGenerate"]>[0] = { prompt: [{ role: "user", content: [{ type: "text", text: "Read evidence" }] }] };
type Finish = Extract<Awaited<ReturnType<Model["doGenerate"]>>["stream"] extends ReadableStream<infer Part> ? Part : never, { type: "finish" }>;
function streamModel(usage: unknown, content = true) {
  const model: Model = { specificationVersion: "v2", provider: "offline-budget", modelId: "budget", supportedUrls: {},
    doGenerate: vi.fn(async () => ({ stream: new ReadableStream({ start(controller) {
      if (content) controller.enqueue({ type: "finish", finishReason: "stop", usage: usage as Finish["usage"] });
      controller.close();
    } }) })), doStream: vi.fn(() => { throw new Error("Unexpected stream call"); }) };
  return model;
}
async function consume(model: Model, input = request): Promise<void> {
  const response = await model.doGenerate(input);
  for await (const _part of response.stream) { /* Consume usage guards as Mastra does. */ }
}

// Real token fields from the three Batch 1 responses, as projected by Mastra.
it.each([
  ["blocked-multiple", 945, 151],
  ["non-first-pickup", 947, 192],
  ["tradeoff-pickup", 945, 167],
])("normalizes captured %s usage and forwards its metadata intact", async (_case, input, output) => {
  const usage = {
    inputTokens: { total: input, noCache: input, cacheRead: 0, cacheWrite: 0 },
    outputTokens: { total: output, text: output, reasoning: 0 },
    raw: { input_tokens: input, output_tokens: output, input_tokens_details: { cached_tokens: 0, cache_write_tokens: 0 } },
  };
  const original = structuredClone(usage);
  const budget = new InvestigationBudget();
  try {
    expect(normalizeInvestigationUsage(usage)).toEqual({ inputTokens: input, outputTokens: output, totalTokens: input + output });
    const result = await budget.model(streamModel(usage), () => {}).doGenerate(request);
    for await (const part of result.stream) if (part.type === "finish") expect(part.usage).toBe(usage);
    expect(usage).toEqual(original);
    expect(budget.trace).toMatchObject({ inputTokens: input, outputTokens: output, totalTokens: input + output, usageReports: 1 });
  } finally { budget.close(); }
});

it("counts cache and reasoning as subsets and preserves explicit flat totals", () => {
  expect(normalizeInvestigationUsage({
    inputTokens: { total: 100, noCache: 60, cacheRead: 30, cacheWrite: 10 },
    outputTokens: { total: 20, text: 15, reasoning: 5 },
    raw: { input_tokens: 100, output_tokens: 20, total_tokens: 120,
      input_tokens_details: { cached_tokens: 30, cache_write_tokens: 10 }, output_tokens_details: { reasoning_tokens: 5 } },
  })).toEqual({ inputTokens: 100, outputTokens: 20, totalTokens: 120 });
  expect(normalizeInvestigationUsage({ inputTokens: 100, outputTokens: 20, totalTokens: 125, cachedInputTokens: 30, reasoningTokens: 25 }))
    .toEqual({ inputTokens: 100, outputTokens: 20, totalTokens: 125 });
});

it.each([
  undefined,
  { inputTokens: 1, outputTokens: 1 },
  { inputTokens: { total: 1 }, outputTokens: {} },
  { inputTokens: { total: 1 }, outputTokens: 1, totalTokens: 2 },
  { inputTokens: { total: "1" }, outputTokens: { total: 1 } },
  { inputTokens: { total: -1 }, outputTokens: { total: 1 } },
  { inputTokens: { total: Number.MAX_SAFE_INTEGER }, outputTokens: { total: 1 } },
  { inputTokens: { total: 5, cacheRead: 4, cacheWrite: 2 }, outputTokens: { total: 1 } },
  { inputTokens: { total: 5 }, outputTokens: { total: 2, reasoning: 3 } },
  { inputTokens: { total: 5, cacheRead: null }, outputTokens: { total: 1 } },
  { inputTokens: { total: 5 }, outputTokens: { total: 1 }, totalTokens: 6, raw: { total_tokens: 7 } },
  { inputTokens: { total: 5 }, outputTokens: { total: 1 }, raw: { total_tokens: 5 } },
  { inputTokens: { total: 5 }, outputTokens: { total: 1 }, raw: { total_tokens: null } },
  { inputTokens: { total: 5 }, outputTokens: { total: 1 }, raw: { input_tokens: 4 } },
  { inputTokens: { total: 5, cacheRead: 2 }, outputTokens: { total: 1 }, raw: { input_tokens_details: { cached_tokens: 1 } } },
])("rejects missing, inconsistent or malformed usage: %j", usage => {
  expect(normalizeInvestigationUsage(usage)).toBeUndefined();
});

it("hard-caps actual provider invocations across repeated, concurrent and hidden extra calls", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 1, outputTokens: 1, totalTokens: 2 });
  try {
    const model = budget.model(provider, () => {});
    for (let i = 0; i < 6; i++) await consume(model);
    await expect(consume(model)).rejects.toThrow();
    expect(provider.doGenerate).toHaveBeenCalledTimes(6);
    expect(budget.trace).toMatchObject({ failureCategory: "budget_exhausted", budgetFailure: { guard: "request_limit" } });
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
    expect(budget.trace).toMatchObject({ failureCategory: "budget_exhausted", budgetFailure: { guard: "model_busy" } });
  } finally { budget.close(); }
});
it.each([
  { inputTokens: 32_001, outputTokens: 1, totalTokens: 32_002 },
  { inputTokens: 1, outputTokens: 6001, totalTokens: 6002 },
  { inputTokens: 1, outputTokens: 1, totalTokens: 44_001 },
  { inputTokens: undefined, outputTokens: 1, totalTokens: 2 },
  { inputTokens: 1, outputTokens: -1, totalTokens: 0 },
  { inputTokens: 10, outputTokens: 10, totalTokens: 2 },
  { inputTokens: { total: 32_001 }, outputTokens: { total: 1 } },
  { inputTokens: { total: 1 }, outputTokens: { total: 6001 } },
  { inputTokens: { total: 1 }, outputTokens: { total: 1 }, raw: { total_tokens: 44_001 } },
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
    expect(budget.trace).toMatchObject({ failureCategory: "budget_exhausted", budgetFailure: { guard: "prompt_size", promptBytes: expect.any(Number) } });
  } finally { budget.close(); }
});
it("rejects missing usage/finish metadata", async () => {
  const budget = new InvestigationBudget();
  try { await expect(consume(budget.model(streamModel({}, false), () => {}))).rejects.toThrow(); }
  finally { budget.close(); }
});
it("captures actual reported model identity separately from the requested adapter identity", async () => {
  const budget = new InvestigationBudget(), model = streamModel({ inputTokens: 1, outputTokens: 1, totalTokens: 2 });
  model.doGenerate = async () => ({ stream: new ReadableStream({ start(controller) {
    controller.enqueue({ type: "response-metadata", modelId: "actual-v1", id: "response", timestamp: new Date() });
    controller.enqueue({ type: "finish", finishReason: "stop", usage: { inputTokens: 1, outputTokens: 1, totalTokens: 2 } }); controller.close();
  } }) });
  try { await consume(budget.model(model, () => {})); expect(budget.trace.reportedActualModel).toBe("offline-budget/actual-v1"); expect(model.modelId).toBe("budget"); }
  finally { budget.close(); }
});
it.each(["unknown usage", "excessive usage"])("never submits after %s from an actual Mastra model response", async mode => {
  const base = offlineModelFixture(null), model = { ...base, async doGenerate() {
    const result = await base.doGenerate();
    return { ...result, usage: mode === "unknown usage" ? { inputTokens: undefined, outputTokens: undefined, totalTokens: undefined }
      : { inputTokens: 33_000, outputTokens: 1, totalTokens: 33_001 } };
  } };
  const api = { readFleet: vi.fn().mockResolvedValue(recoveryDecisionState()), submitCharging: vi.fn().mockResolvedValue(pendingFixture),
    submitReassignment: vi.fn().mockResolvedValue(pendingFixture),
    startAgentRun: vi.fn(async (observationId: string) => ({ id: "Arun", runtimeId: "runtime", observationId })),
    finishAgentRun: vi.fn(async (id: string) => ({ runId: id, recorded: true as const, denial: "", proposalResult: null })) };
  const result = await runRecoveryDecision(api, { enabled: true, model, source: "mocked", objective: "compare_tradeoffs", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("failed"); expect(result.aiAnalysis.decision).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(investigationLimits.requests).toBe(6);
});

it("distinguishes exhausted output allowance from prompt-size preflight", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 1, outputTokens: 6000, totalTokens: 6001 });
  try {
    const model = budget.model(provider, () => {});
    await consume(model); await consume(model);
    await expect(consume(model)).rejects.toThrow();
    expect(provider.doGenerate).toHaveBeenCalledTimes(2);
    expect(budget.trace).toMatchObject({ failureCategory: "budget_exhausted", budgetFailure: { guard: "output_allowance" } });
  } finally { budget.close(); }
});

it("identifies a provider report that crosses the aggregate token ceiling", async () => {
  const budget = new InvestigationBudget(), provider = streamModel({ inputTokens: 32_001, outputTokens: 1, totalTokens: 32_002 });
  try {
    await expect(consume(budget.model(provider, () => {}))).rejects.toThrow();
    expect(provider.doGenerate).toHaveBeenCalledOnce();
    expect(budget.trace).toMatchObject({ failureCategory: "budget_exhausted", budgetFailure: { guard: "reported_usage" } });
  } finally { budget.close(); }
});
