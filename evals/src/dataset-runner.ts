import { isDeepStrictEqual } from "node:util";
import { reasoningSchema, snapshotReferenceSchema } from "../../agent/src/contracts.js";
import { runEvaluation, type EvaluationReport, type OfflineAdapter, type ScoringResult } from "./index.js";
import type { Scenario } from "./dataset.js";
import { injectProbe, observerAdapter } from "./dataset-adapter.js";

export interface CategoryResult {
  decisions: number;
  recommendationsPassed: number;
  recommendationsUnsuccessful: number;
  probes: number;
  expectedRejections: number;
  expectationMismatches: number;
  metrics: EvaluationReport["metrics"];
}
export interface DatasetReport {
  datasetVersion: "p5-01-v1";
  evaluation: EvaluationReport;
  scenarios: { id: string; description: string; category: string; inputKind: Scenario["inputKind"];
    provenance: string; probe: Scenario["probe"]; expectedScoring: Scenario["expectedScoring"];
    actual: ScoringResult; expectationMatched: boolean; metadataIssues: string[] }[];
  categories: Record<string, CategoryResult>;
  totals: CategoryResult;
}
export function metadataIssues(test: Scenario): string[] {
  const issues: string[] = [];
  const action = test.expected?.action;
  const [charging, reassignment] = reasoningSchema.shape.proposedAction.unwrap().options;
  const identities = [charging.omit({ evidence: true, rationale: true }), reassignment.omit({ evidence: true, rationale: true })];
  if (!snapshotReferenceSchema.array().safeParse(test.expected?.incidents).success ||
    (action !== null && !identities.some(schema => schema.safeParse(action).success))) issues.push("Ground-truth labels incomplete or malformed.");
  for (const field of ["id", "description", "category", "provenance"] as const) {
    if (typeof test[field] !== "string" || !test[field].trim()) issues.push(`${field} missing.`);
  }
  for (const field of ["requiredEvidence", "forbiddenRecommendations"] as const) {
    if (!test[field]?.length || test[field].some(s => typeof s !== "string" || !s.trim())) issues.push(`${field} incomplete.`);
  }
  if (test.requiresHumanApproval !== (action !== null)) issues.push("Human approval expectation inconsistent.");
  if (!["existing-tested-fixture", "synthetic-reasoning-only"].includes(test.inputKind)) issues.push("Input provenance kind missing.");
  if (!["none", "stale_reference", "fabricated_candidate", "missing_evidence", "unsupported_command"].includes(test.probe)) issues.push("Unknown probe.");
  if (!test.expectedScoring || !["passed", "failed", "malformed"].includes(test.expectedScoring.status) ||
    ["incidentReferences", "evidenceGrounding", "recommendationValidity", "unsupportedCommands", "operatorAttention", "appropriateAbstention"]
      .some(key => !["pass", "fail", "not_applicable"].includes(test.expectedScoring?.checks?.[key as keyof ScoringResult["checks"]]))) issues.push("Scoring expectation incomplete.");
  if ((test.probe === "none") !== (test.expectedScoring?.status === "passed")) issues.push("Decision/probe scoring expectation inconsistent.");
  return issues;
}
const emptyCounts = (): CategoryResult => ({ decisions: 0, recommendationsPassed: 0, recommendationsUnsuccessful: 0,
  probes: 0, expectedRejections: 0, expectationMismatches: 0, metrics: {
    incidentReferences: { passed: 0, failed: 0, notApplicable: 0 }, evidenceGrounding: { passed: 0, failed: 0, notApplicable: 0 },
    recommendationValidity: { passed: 0, failed: 0, notApplicable: 0 }, unsupportedCommands: { passed: 0, failed: 0, notApplicable: 0 },
    operatorAttention: { passed: 0, failed: 0, notApplicable: 0 }, appropriateAbstention: { passed: 0, failed: 0, notApplicable: 0 },
  } });

export async function runDataset(cases: Scenario[], adapter: OfflineAdapter = observerAdapter): Promise<DatasetReport> {
  const probes = new Map(cases.map(c => [c.id, c.probe]));
  const checkedCases = cases.map(c => metadataIssues(c).length ? { ...c, provenance: "" } : c);
  const evaluation = await runEvaluation(checkedCases, async (input, id) => {
    const actual = await adapter(input, id);
    return { reasoning: injectProbe(actual.reasoning, probes.get(id) ?? "none") };
  });
  const scenarios = cases.map((test, index) => {
    const actual = evaluation.results[index]; const issues = metadataIssues(test);
    return { id: test.id, description: test.description, category: test.category, inputKind: test.inputKind,
      provenance: test.provenance, probe: test.probe, expectedScoring: test.expectedScoring, actual,
      expectationMatched: issues.length === 0 && isDeepStrictEqual({ status: actual.status, checks: actual.checks }, test.expectedScoring), metadataIssues: issues };
  });
  const categories: Record<string, CategoryResult> = {};
  const totals = emptyCounts();
  for (const result of scenarios) {
    const category = categories[result.category] ??= emptyCounts();
    for (const counts of [category, totals]) {
      for (const key of Object.keys(counts.metrics) as (keyof ScoringResult["checks"])[]) {
        const status = result.actual.checks[key];
        counts.metrics[key][status === "pass" ? "passed" : status === "fail" ? "failed" : "notApplicable"]++;
      }
      if (!result.expectationMatched) counts.expectationMismatches++;
      if (result.probe === "none") {
        counts.decisions++;
        if (result.actual.status === "passed") counts.recommendationsPassed++;
        else counts.recommendationsUnsuccessful++;
      } else {
        counts.probes++;
        if (result.expectationMatched && ["failed", "malformed"].includes(result.actual.status)) counts.expectedRejections++;
      }
    }
  }
  return { datasetVersion: "p5-01-v1", evaluation, scenarios, categories, totals };
}
export function summarizeDataset(report: DatasetReport): string {
  return [`Raw scorer outcomes: ${report.evaluation.totals.passed} passed; ${report.evaluation.totals.unsuccessful} unsuccessful (includes injected probes).`,
    `Fleet decisions: ${report.totals.recommendationsPassed}/${report.totals.decisions} passed. Synthetic probes: ${report.totals.expectedRejections}/${report.totals.probes} rejected as expected.`,
    ...report.scenarios.map(s => `${s.id} [${s.category}]: ${s.actual.status}; ${s.expectationMatched ? "expectation matched" : "EXPECTATION MISMATCH"}; recommendationValidity=${s.actual.checks.recommendationValidity}; unsupportedCommands=${s.actual.checks.unsupportedCommands}`),
    ...Object.entries(report.categories).map(([name, c]) => `${name}: decisions ${c.recommendationsPassed}/${c.decisions}; expected probe rejections ${c.expectedRejections}/${c.probes}; expectation mismatches ${c.expectationMismatches}`),
    "Zero applicable cases is not a success rate. Probe rejections are not successful agent recommendations.",
    "Proposal submission, execution, delivery and live LLM accuracy: not measured."].join("\n");
}
