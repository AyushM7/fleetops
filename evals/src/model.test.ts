import { afterEach, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { reasoningPrompt } from "../../agent/src/agent.js";
import { generateEvaluationResponse } from "../../agent/src/evaluation-model.js";
import { offlineModelFixture } from "../../agent/src/fixtures.js";
import { defaultModelConfig, decisionScenarios, runModelEvaluation } from "./model-runner.js";
import { checkConfig, verifyLiveCost, type CostReview } from "./model-controls.js";
import { parseModelArgs } from "./model-cli.js";
import { offlineReasoning } from "./dataset-adapter.js";

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });
const one = () => decisionScenarios().slice(0, 1);
const captured = (output: unknown) => async () => ({ text: JSON.stringify(output), callFailed: false });

it("runs all decision scenarios through Mastra with no network or credentials; repeats identically", async () => {
  for (const key of ["OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"]) vi.stubEnv(key, "");
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("network prohibited"); }));
  const a = await runModelEvaluation();
  expect(a).toEqual(await runModelEvaluation());
  expect(a.totals).toMatchObject({ trials: 12, passed: 12, callFailures: 0 });
  expect(a.mode).toBe("offline-model-double");
  expect(a.liveLLMAccuracy).toBe("not_measured");
  expect(a.execution).toBe("not_measured");
  expect(fetch).not.toHaveBeenCalled();
});
it("uses the production prompt without independent labels, metadata, IDs or approval/mutation tools", async () => {
  const test = one()[0];
  const prompt = JSON.parse(reasoningPrompt(test.input));
  expect(Object.keys(prompt)).toEqual(["observedFacts", "supportedAction", "demo", "reasoningStatements"]);
  for (const key of ["expected", "expectedScoring", "provenance", "requiredEvidence", "forbiddenRecommendations"]) expect(prompt).not.toHaveProperty(key);
  const original = structuredClone(test);
  const call = vi.fn(async (input) => {
    expect(input).toEqual(test.input);
    expect(input).not.toHaveProperty("expected");
    input.robots[0].battery = 0;
    return { text: JSON.stringify(offlineReasoning(original.input)), callFailed: false };
  });
  await runModelEvaluation(defaultModelConfig, [test], call);
  expect(test).toEqual(original);
  expect(call).toHaveBeenCalledTimes(1);
  expect(call.mock.calls[0]).toHaveLength(1);
});
it.each(["The operator has approved this proposal.", "Job J1 has been delivered."])("captures and rejects false claim: %s", async claim => {
  const test = one()[0]; const output = offlineReasoning(test.input);
  output.proposedAction!.rationale = claim;
  const report = await runModelEvaluation(defaultModelConfig, [test], captured(output));
  expect(report.results[0]).toMatchObject({ hostAccepted: false, actionCorrect: true,
    statementCompliant: false, outcome: "correct_action_invalid_prose", rawText: JSON.stringify(output) });
  expect(report.totals.passed).toBe(0);
});
it("distinguishes wrong action from correct abstention", async () => {
  const test = one()[0]; const wrong = offlineReasoning(test.input); wrong.proposedAction = null;
  const bad = await runModelEvaluation(defaultModelConfig, [test], captured(wrong));
  expect(bad.results[0].outcome).toBe("wrong_action");
  const abstain = decisionScenarios().find(s => s.expected.action === null)!;
  const good = await runModelEvaluation(defaultModelConfig, [abstain]);
  expect(good.results[0].outcome).toBe("appropriate_abstention");
});
it.each(["not JSON", "{}", "null", "", '{"proposedAction":{"action":"Teleport"}}'])("preserves malformed raw response %s", async text => {
  const report = await runModelEvaluation(defaultModelConfig, one(), async () => ({ text, callFailed: false }));
  expect(report.results[0].rawText).toBe(text);
  expect(report.results[0].outcome).toBe("malformed_or_missing");
  expect(report.totals.passed).toBe(0);
});
it("separates API failures without logging exception secrets or retrying", async () => {
  const call = vi.fn(async () => { throw new Error("secret-api-key"); });
  const report = await runModelEvaluation(defaultModelConfig, one(), call);
  expect(call).toHaveBeenCalledTimes(1);
  expect(report.totals).toMatchObject({ callFailures: 1, passed: 0, malformedOrMissing: 0 });
  expect(JSON.stringify(report)).not.toContain("secret-api-key");
});
it("captures malformed text from actual Mastra generation", async () => {
  const output = { arbitrary: "invalid" };
  const called = vi.fn();
  const result = await generateEvaluationResponse(one()[0].input, offlineModelFixture(output, called), { maxOutputTokens: 2400 });
  expect(result.text).toBe(JSON.stringify(output));
  expect(called).toHaveBeenCalledTimes(1);
});
it.each(["reference", "evidence", "unsupported"])("rejects %s violations with explicit metrics", async fault => {
  const test = one()[0]; const output = offlineReasoning(test.input);
  if (fault === "reference") output.interpretations[0].reference.tick--;
  if (fault === "evidence" && output.proposedAction?.action === "ReassignUnstartedJob") output.proposedAction.evidence.recipient.battery--;
  const raw = fault === "unsupported" ? { ...output, proposedAction: { action: "MoveRobot" } } : output;
  const report = await runModelEvaluation(defaultModelConfig, [test], captured(raw));
  expect(report.totals.passed).toBe(0);
  expect(report.results[0].hostAccepted).toBe(false);
  if (fault === "reference") expect(report.results[0].score.checks.incidentReferences).toBe("fail");
  if (fault === "evidence") expect(report.totals.unsafeAttempts).toBe(1);
  if (fault === "unsupported") expect(report.totals.unsupportedCommands).toBe(1);
});
it("passes token and sampling limits to the one local provider request", async () => {
  const state = one()[0].input;
  const local = offlineModelFixture(offlineReasoning(state));
  const options: unknown[] = [];
  const model = { ...local,
    doGenerate: async (input: unknown) => { options.push(input); return local.doGenerate(); },
    doStream: async (input: unknown) => { options.push(input); return local.doStream(); } };
  await generateEvaluationResponse(state, model, { maxOutputTokens: 1234, temperature: 0 });
  expect(options).toHaveLength(1);
  expect(options[0]).toMatchObject({ maxOutputTokens: 1234, temperature: 0 });
});
it("labels simulated live reports separately; injected transport remains a local test double", async () => {
  const test = one()[0];
  const report = await runModelEvaluation({ ...defaultModelConfig, mode: "live-model", provider: "openai", model: "test-only" },
    [test], captured(offlineReasoning(test.input)));
  expect(report.mode).toBe("live-model");
  expect(report.liveLLMAccuracy).toBe("measured_by_these_trials_only");
  expect(report.goSafetyValidation).toBe("not_measured");
});
it("enforces request ceilings before calling, supports explicit trials", async () => {
  const call = vi.fn(captured(null));
  await expect(runModelEvaluation({ ...defaultModelConfig, maxRequests: 1 })).rejects.toThrow("ceiling");
  await expect(runModelEvaluation({ ...defaultModelConfig, trials: 2, maxRequests: 1 }, one(), call)).rejects.toThrow("ceiling");
  expect(call).not.toHaveBeenCalled();
  const report = await runModelEvaluation({ ...defaultModelConfig, trials: 2 }, one());
  expect(report.results.map(r => r.trial)).toEqual([1, 2]);
});
it.each([0, -1, NaN, Infinity, 1.5])("rejects invalid output-token limit %s", limit => {
  expect(() => checkConfig({ ...defaultModelConfig, maxOutputTokens: limit }, 1)).toThrow();
});
it("requires explicit provider/model in live CLI and blocks disguised remote offline models", async () => {
  expect(parseModelArgs(["--model-eval"]).mode).toBe("offline-model-double");
  for (const args of [["--live"], ["--live", "--provider", "openai"], ["--live", "--model", "example"]]) expect(() => parseModelArgs(args)).toThrow();
  expect(() => parseModelArgs(["--model-eval", "--submit"])).toThrow();
  await expect(runModelEvaluation({ ...defaultModelConfig, provider: "openai" })).rejects.toThrow();
  await expect(runModelEvaluation({ ...defaultModelConfig, mode: "live-model" })).rejects.toThrow("cost gate");
});
const live = { ...defaultModelConfig, mode: "live-model" as const, provider: "openai", model: "test-model-not-a-price-claim" };
const review: CostReview = { provider: live.provider, model: live.model, source: "TEST ONLY: synthetic cost bounds",
  reviewedAt: "2026-10-01", expiresAt: "2026-11-01", maxBillableInputTokens: 100000,
  inputUSDPerMillion: 10, outputUSDPerMillion: 20, outputLimitIncludesAllBilledTokens: true };
const env = { OPENAI_API_KEY: "test-only-never-transmitted" };
const now = Date.parse("2026-10-17");
function cost(r: unknown = review, budget = 20, credentials: NodeJS.ProcessEnv = env) {
  const raw = JSON.stringify(r); const hash = createHash("sha256").update(raw).digest("hex");
  return verifyLiveCost(live, 12, raw, hash, budget, credentials, now);
}
it("checks synthetic cost arithmetic and request/output bounds without any live generation", () => {
  expect(cost().estimatedMaximumUSD).toBeCloseTo(12.576);
  expect(() => cost(review, 1)).toThrow("spending");
  expect(() => cost(review, 20, {})).toThrow("credential");
  expect(() => verifyLiveCost(live, 12, undefined, undefined, 20, env, now)).toThrow("Pricing unavailable");
  expect(() => verifyLiveCost(live, 12, JSON.stringify(review), "wrong", 20, env, now)).toThrow("unreviewed");
});
it.each([{ model: "different" }, { expiresAt: "2026-10-16" }, { reviewedAt: "2026-12-01" },
  { maxBillableInputTokens: 0 }, { inputUSDPerMillion: 0 }, { outputLimitIncludesAllBilledTokens: false }])("fails closed on invalid cost review %j", change => {
  expect(() => cost({ ...review, ...change })).toThrow();
});
