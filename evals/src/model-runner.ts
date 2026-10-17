import { reasoningSchema } from "../../agent/src/contracts.js";
import { validateReasoning } from "../../agent/src/reasoning.js";
import { reasoningTextGrounded } from "../../agent/src/statements.js";
import { offlineModelFixture } from "../../agent/src/fixtures.js";
import { generateEvaluationResponse, type CapturedResponse } from "../../agent/src/evaluation-model.js";
import type { FleetState } from "../../agent/src/contracts.js";
import { dataset, type Scenario } from "./dataset.js";
import { metadataIssues } from "./dataset-runner.js";
import { offlineReasoning } from "./dataset-adapter.js";
import { scoreCase } from "./index.js";
import { checkConfig, type ModelConfig } from "./model-controls.js";

export const defaultModelConfig: ModelConfig = { mode: "offline-model-double", provider: "offline-fixture",
  model: "deterministic", trials: 1, maxRequests: 12, maxOutputTokens: 2400 };
export type Generate = (input: FleetState) => Promise<CapturedResponse>;
export const decisionScenarios = (): Scenario[] => dataset().filter(s => s.probe === "none");

function measure(test: Scenario, captured: CapturedResponse) {
  let raw: unknown;
  try { raw = captured.text === null ? undefined : JSON.parse(captured.text); } catch { raw = captured.text; }
  const score = scoreCase(test, { reasoning: raw });
  const parsed = reasoningSchema.safeParse(raw);
  let hostAccepted = false;
  if (!captured.callFailed) { try { validateReasoning(raw, test.input); hostAccepted = true; } catch { /* recorded below */ } }
  const actionCorrect = !captured.callFailed && parsed.success ? score.checks.recommendationValidity === "pass" : null;
  const statementCompliant = !captured.callFailed && parsed.success ? reasoningTextGrounded(parsed.data, test.input) : null;
  const command = typeof raw === "object" && raw !== null && "proposedAction" in raw ? raw.proposedAction : undefined;
  const unsupportedCommand = command !== undefined && command !== null && typeof command === "object" &&
    "action" in command && !["SetCharging", "ReassignUnstartedJob"].includes(String(command.action));
  return { rawText: captured.text, rawRecommendation: raw ?? null, score,
    callStatus: captured.callFailed ? "generation_call_failure" : "completed",
    hostAccepted, actionCorrect, statementCompliant,
    correctActionInvalidProse: actionCorrect === true && statementCompliant === false,
    unsupportedCommand,
    unsafeRecommendationAttempt: !captured.callFailed && parsed.success && statementCompliant && parsed.data.proposedAction !== null ? !hostAccepted : null,
    fabricatedApprovalOrDelivery: "not_independently_classified" as const,
    // Exact statement violations include fabricated claims and harmless paraphrases; do not conflate them.
    outcome: captured.callFailed ? "call_failure" : !parsed.success ? "malformed_or_missing" :
      !actionCorrect ? "wrong_action" : !statementCompliant ? "correct_action_invalid_prose" :
        !hostAccepted ? "host_rejected" : test.expected.action === null ? "appropriate_abstention" : "passed" };
}
export async function runModelEvaluation(config: ModelConfig = defaultModelConfig,
  cases: Scenario[] = decisionScenarios(), generate?: Generate) {
  checkConfig(config, cases.length);
  if (config.mode === "live-model" && !generate) throw new Error("Live execution requires the CLI cost gate.");
  if (cases.some(s => metadataIssues(s).length || s.probe !== "none") || new Set(cases.map(s => s.id)).size !== cases.length) throw new Error("Invalid model evaluation dataset.");
  const call = generate ?? (async input => generateEvaluationResponse(input,
    offlineModelFixture(offlineReasoning(input)), { maxOutputTokens: config.maxOutputTokens, temperature: config.temperature }));
  const results: ({ id: string; category: string; trial: number; baseline: ReturnType<typeof scoreCase> } & ReturnType<typeof measure>)[] = [];
  for (const test of cases) {
    const baseline = scoreCase(test, { reasoning: offlineReasoning(test.input) });
    for (let trial = 1; trial <= config.trials; trial++) {
      let captured: CapturedResponse;
      try { captured = await call(structuredClone(test.input)); } catch { captured = { text: null, callFailed: true }; }
      results.push({ id: test.id, category: test.category, trial, baseline, ...measure(test, captured) });
    }
  }
  const count = (items: typeof results) => ({ trials: items.length,
    passed: items.filter(r => r.score.status === "passed" && r.hostAccepted && r.callStatus === "completed").length,
    callFailures: items.filter(r => r.callStatus !== "completed").length,
    deterministicBaselinePassed: items.filter(r => r.baseline.status === "passed").length,
    malformedOrMissing: items.filter(r => r.outcome === "malformed_or_missing").length,
    actionCorrect: items.filter(r => r.actionCorrect === true).length,
    actionMeasured: items.filter(r => r.actionCorrect !== null).length,
    hostAccepted: items.filter(r => r.hostAccepted).length,
    correctActionInvalidProse: items.filter(r => r.correctActionInvalidProse).length,
    statementViolations: items.filter(r => r.statementCompliant === false).length,
    statementMeasured: items.filter(r => r.statementCompliant !== null).length,
    unsupportedCommands: items.filter(r => r.unsupportedCommand).length,
    unsafeAttempts: items.filter(r => r.unsafeRecommendationAttempt === true).length,
    unsafeAttemptMeasured: items.filter(r => r.unsafeRecommendationAttempt !== null).length,
    metrics: Object.fromEntries(Object.keys(items[0]!.score.checks).map(key => {
      const metric = key as keyof typeof items[number]["score"]["checks"];
      return [key, { passed: items.filter(r => r.callStatus === "completed" && r.score.checks[metric] === "pass").length,
        failed: items.filter(r => r.callStatus === "completed" && r.score.checks[metric] === "fail").length,
        notApplicable: items.filter(r => r.callStatus === "completed" && r.score.checks[metric] === "not_applicable").length,
        callFailures: items.filter(r => r.callStatus !== "completed").length }];
    })) });
  return { version: "p5-04-v1", mode: config.mode, configuration: config, results, totals: count(results),
    categories: Object.fromEntries([...new Set(results.map(r => r.category))].map(c => [c, count(results.filter(r => r.category === c))])),
    liveLLMAccuracy: config.mode === "live-model" && results.some(r => r.callStatus === "completed") ? "measured_by_these_trials_only" : "not_measured",
    callFailureClassification: "provider_transport_or_generation_pipeline; separate_from_model_output_failures",
    proposalSubmission: "not_measured", goSafetyValidation: "not_measured", execution: "not_measured", delivery: "not_measured",
    recoveryEffectiveness: "separate_p5-03_benchmark" };
}
export function summarizeModel(report: Awaited<ReturnType<typeof runModelEvaluation>>): string {
  return [`${report.mode}: ${report.totals.passed}/${report.totals.trials} trials passed; ${report.totals.callFailures} call failures.`,
    ...report.results.map(r => `${r.id} trial ${r.trial}: ${r.outcome}; host=${r.hostAccepted ? "accepted" : "rejected"}`),
    "Go validation, proposal submission, execution and delivery: not measured. Recovery: separate P5-03 benchmark.",
    report.mode === "offline-model-double" ? "Real LLM accuracy: not measured." : "Live trials are variable, not deterministic."].join("\n");
}
