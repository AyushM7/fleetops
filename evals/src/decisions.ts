import type { FleetState } from "../../agent/src/contracts.js";
import type { AbstentionReason, DecisionObjective, RecoveryDecision } from "../../agent/src/decision-contracts.js";
import { decisionObjectiveSchema } from "../../agent/src/decision-contracts.js";
import { validateRecoveryDecision } from "../../agent/src/decision.js";
import { fleetFixture, recoveryDecisionFixture, recoveryDecisionState } from "../../agent/src/fixtures.js";
import { observeFleet } from "../../agent/src/observe.js";
import { validateSnapshot } from "../../agent/src/reasoning.js";

export interface DecisionCase {
  id: string; provenance: "synthetic-reasoning-only"; objective: DecisionObjective; input: FleetState;
  expected: { recipients: string[]; abstention: AbstentionReason | null; reject: boolean };
  scriptedResponse: unknown;
}
function decisionCase(id: string, input: FleetState, objective: DecisionObjective, recipient: string | null,
  recipients: string[], abstention: AbstentionReason | null = null, basis?: Extract<RecoveryDecision["decision"], { kind: "select" }>["basis"]): DecisionCase {
  return { id, provenance: "synthetic-reasoning-only", input, objective, expected: { recipients, abstention, reject: false },
    scriptedResponse: recoveryDecisionFixture(input, objective, recipient, basis) };
}
export function decisionCases(): DecisionCase[] {
  const poisoned = recoveryDecisionState(); poisoned.poisoned = true;
  const cases = [
    decisionCase("non-first-pickup-minimum", recoveryDecisionState(), "minimize_pickup_steps", "R4", ["R4"]),
    decisionCase("tradeoff-route-objective", recoveryDecisionState("tradeoff"), "minimize_pickup_steps", "R3", ["R3"]),
    decisionCase("tradeoff-battery-objective", recoveryDecisionState("tradeoff"), "preserve_battery_headroom", "R4", ["R4"]),
    decisionCase("tradeoff-balanced-selection", recoveryDecisionState("tradeoff"), "compare_tradeoffs", "R5", ["R3", "R4", "R5"], "operator_tradeoff", "tradeoff"),
    decisionCase("equal-cost-alternatives", recoveryDecisionState("tie"), "minimize_pickup_steps", "R4", ["R3", "R4"]),
    decisionCase("no-eligible-candidate", recoveryDecisionState("none"), "compare_tradeoffs", null, [], "no_candidates"),
    decisionCase("operator-tradeoff-abstention", recoveryDecisionState("tradeoff"), "compare_tradeoffs", null, ["R3", "R4", "R5"], "operator_tradeoff"),
    decisionCase("healthy-abstention", fleetFixture(), "compare_tradeoffs", null, [], "no_supported_incident"),
    decisionCase("poisoned-abstention", poisoned, "compare_tradeoffs", null, [], "runtime_poisoned"),
  ];
  for (const order of [[0, 1, 2], [0, 2, 1], [1, 0, 2], [1, 2, 0], [2, 0, 1], [2, 1, 0]]) {
    const state = recoveryDecisionState(); state.reassignmentCandidates = order.map(i => state.reassignmentCandidates[i]);
    cases.push(decisionCase(`candidate-order-${order.join("")}`, state, "minimize_pickup_steps", "R4", ["R4"]));
  }
  for (const probe of ["fabricated-evidence", "false-comparison"] as const) {
    const row = decisionCase(probe, recoveryDecisionState(), "minimize_pickup_steps", "R4", []);
    const response = row.scriptedResponse as RecoveryDecision;
    if (probe === "fabricated-evidence") response.considered[1].candidate.requiredBattery++;
    else response.comparisons[0].relation = "greater";
    row.expected.reject = true; cases.push(row);
  }
  return cases;
}
function matchesOutcome(test: DecisionCase, output: RecoveryDecision): boolean {
  if (test.expected.reject) return false;
  return output.decision.kind === "select" ? test.expected.recipients.includes(output.decision.selected.toRobotId)
    : test.expected.abstention === output.decision.reason;
}
export function scoreRecoveryDecision(test: DecisionCase, raw: unknown) {
  try {
    const output = validateRecoveryDecision(raw, test.input, test.objective);
    return { grounded: true, expectationMet: matchesOutcome(test, output), outcome: output.decision.kind,
      selectedRecipient: output.decision.kind === "select" ? output.decision.selected.toRobotId : null };
  } catch {
    return { grounded: false, expectationMet: test.expected.reject, outcome: "rejected", selectedRecipient: null };
  }
}
export async function runDecisionEvaluation(cases = decisionCases(), generate?: (state: FleetState, objective: DecisionObjective) => Promise<unknown>) {
  if (!cases.length || new Set(cases.map(c => c.id)).size !== cases.length) throw new Error("Unique nonempty decision cases required.");
  const results = [];
  for (const test of cases) {
    validateSnapshot(test.input); decisionObjectiveSchema.parse(test.objective);
    if (!test.input.observationId || test.provenance !== "synthetic-reasoning-only") throw new Error("Versioned fixture identity and provenance required.");
    const baseline = observeFleet(test.input).recommendedAction;
    const baselineRecipient = baseline?.action === "ReassignUnstartedJob" ? baseline.toRobotId : null;
    const baselineMatches = test.expected.reject ? null : baselineRecipient === null ? test.expected.abstention !== null : test.expected.recipients.includes(baselineRecipient);
    let score: ReturnType<typeof scoreRecoveryDecision>;
    try { score = scoreRecoveryDecision(test, generate ? await generate(structuredClone(test.input), test.objective) : structuredClone(test.scriptedResponse)); }
    catch { score = { grounded: false, expectationMet: false, outcome: "generation_failed", selectedRecipient: null }; }
    results.push({ id: test.id, objective: test.objective, provenance: test.provenance, ...score, baselineRecipient, baselineMatches });
  }
  return { version: "recovery-decision-eval-v2", mode: generate ? "injected-generator" : "offline-scripted-responses", results,
    totals: { cases: results.length, passed: results.filter(r => r.expectationMet).length, failed: results.filter(r => !r.expectationMet).length },
    actualLLM: "not_measured", simulatorEffectiveness: "not_measured", goSafetyAdmission: "not_measured",
    limitation: "Modified Go-shaped snapshots and scripted outputs test decision validation, not model accuracy or scheduling superiority. Expected sets are declared independently of the legacy first-candidate baseline." };
}
