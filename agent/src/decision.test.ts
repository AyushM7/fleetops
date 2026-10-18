import { describe, expect, it } from "vitest";
import { candidateKey, explainRecoveryDecision, validateRecoveryDecision } from "./decision.js";
import { recoveryDecisionFixture, recoveryDecisionState, fleetFixture } from "./fixtures.js";
import type { DecisionMetric, RecoveryDecision } from "./decision-contracts.js";

describe("versioned recovery decisions", () => {
  it("accepts a non-first minimum-pickup choice and verifies concrete arithmetic", () => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
    expect(state.reassignmentCandidates.map(c => c.toRobotId)).toEqual(["R3", "R4", "R5"]);
    expect(validateRecoveryDecision(output, state, "minimize_pickup_steps")).toEqual(output);
    expect(output.considered[1]).toMatchObject({ totalRouteSteps: 8, missionHeadroom: 67, admissionHeadroom: 50 });
    expect(explainRecoveryDecision(output, state, "minimize_pickup_steps")).toContain("pickupSteps 1 less");
  });
  it.each(["R3", "R4", "R5"])("permits admitted choice %s without inventing an optimality claim", recipient => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "compare_tradeoffs", recipient);
    expect(validateRecoveryDecision(output, state, "compare_tradeoffs")).toEqual(output);
    expect(explainRecoveryDecision(output, state, "compare_tradeoffs")).toContain("optimality is not established");
  });
  it.each(["R3", "R4", "R5"])("validates genuine distance/battery trade-offs for %s", recipient => {
    const state = recoveryDecisionState("tradeoff"); const output = recoveryDecisionFixture(state, "compare_tradeoffs", recipient, "tradeoff");
    expect(output.considered.map(e => e.admissionHeadroom)).toEqual([18, 66, 35]);
    expect(validateRecoveryDecision(output, state, "compare_tradeoffs")).toEqual(output);
  });
  it("honors the declared battery objective even when the recipient has a longer pickup", () => {
    const state = recoveryDecisionState("tradeoff");
    const output = recoveryDecisionFixture(state, "preserve_battery_headroom", "R4");
    expect(validateRecoveryDecision(output, state, "preserve_battery_headroom")).toEqual(output);
    expect(() => validateRecoveryDecision(recoveryDecisionFixture(state, "preserve_battery_headroom", "R3"), state, "preserve_battery_headroom")).toThrow("grounding");
    expect(() => validateRecoveryDecision(recoveryDecisionFixture(state, "minimize_pickup_steps", "R4"), state, "minimize_pickup_steps")).toThrow("grounding");
  });
  it.each(["R3", "R4"])("accepts equal-cost minimum %s without a hidden first-candidate tie-break", recipient => {
    const state = recoveryDecisionState("tie");
    expect(validateRecoveryDecision(recoveryDecisionFixture(state, "minimize_pickup_steps", recipient), state, "minimize_pickup_steps").decision.kind).toBe("select");
  });
  it.each([[0, 1, 2], [0, 2, 1], [1, 0, 2], [1, 2, 0], [2, 0, 1], [2, 1, 0]])("accepts independent minimum R4 for ordering %j", (...order) => {
    const state = recoveryDecisionState(); state.reassignmentCandidates = order.map(index => state.reassignmentCandidates[index]);
    const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
    output.considered.reverse(); output.incidents.reverse(); output.operatorAttention.reverse();
    expect(validateRecoveryDecision(output, state, "minimize_pickup_steps")).toEqual(output);
  });
  it.each([
    ["pickupSteps", 1, 6, "less"], ["deliverySteps", 2, 2, "equal"], ["chargingExitSteps", 5, 5, "equal"],
    ["totalRouteSteps", 8, 13, "less"], ["battery", 80, 80, "equal"], ["requiredBattery", 13, 18, "less"],
    ["missionHeadroom", 67, 62, "greater"], ["admissionHeadroom", 50, 50, "equal"],
  ] as const)("verifies available metric %s and reversed comparisons", (metric, leftValue, rightValue, relation) => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "compare_tradeoffs", "R4");
    output.comparisons = output.comparisons.filter(c => c.right.toRobotId !== "R3");
    const left = candidateKey(state.reassignmentCandidates[1]), right = candidateKey(state.reassignmentCandidates[0]);
    output.comparisons.push({ left, right, metric, leftValue, rightValue, relation });
    expect(validateRecoveryDecision(output, state, "compare_tradeoffs")).toEqual(output);
    output.comparisons[2] = { left: right, right: left, metric, leftValue: rightValue, rightValue: leftValue,
      relation: relation === "less" ? "greater" : relation === "greater" ? "less" : "equal" };
    expect(validateRecoveryDecision(output, state, "compare_tradeoffs")).toEqual(output);
  });
  it.each([
    ["foreign observation", (o: RecoveryDecision) => { o.observationId = "d".repeat(64); }],
    ["old tick", (o: RecoveryDecision) => { o.tick--; }],
    ["changed objective", (o: RecoveryDecision) => { o.objective = "compare_tradeoffs"; }],
    ["fabricated recipient", (o: RecoveryDecision) => { if (o.decision.kind === "select") o.decision.selected.toRobotId = "R999"; }],
    ["wrong selected job", (o: RecoveryDecision) => { if (o.decision.kind === "select") o.decision.selected.jobId = "J999"; }],
    ["wrong selected source", (o: RecoveryDecision) => { if (o.decision.kind === "select") o.decision.selected.fromRobotId = "R2"; }],
    ["fabricated candidate metric", (o: RecoveryDecision) => { o.considered[0].candidate.pickupSteps++; }],
    ["wrong recipient telemetry", (o: RecoveryDecision) => { o.considered[1].recipient.battery--; }],
    ["wrong source telemetry", (o: RecoveryDecision) => { o.considered[1].source.waitTicks++; }],
    ["wrong job ownership", (o: RecoveryDecision) => { o.considered[1].job.assignedRobot = "R4"; }],
    ["wrong incident owner", (o: RecoveryDecision) => { o.considered[1].incidentReference.robotId = "R2"; }],
    ["wrong incident type", (o: RecoveryDecision) => { o.considered[1].incidentReference.type = "robot_unresponsive"; }],
    ["old incident reference", (o: RecoveryDecision) => { o.considered[1].incidentReference.tick--; }],
    ["wrong total", (o: RecoveryDecision) => { o.considered[1].totalRouteSteps++; }],
    ["wrong mission headroom", (o: RecoveryDecision) => { o.considered[1].missionHeadroom++; }],
    ["wrong command-floor headroom", (o: RecoveryDecision) => { o.considered[1].admissionHeadroom = 67; }],
    ["fabricated left value", (o: RecoveryDecision) => { o.comparisons[0].leftValue++; }],
    ["fabricated right value", (o: RecoveryDecision) => { o.comparisons[0].rightValue++; }],
    ["false direction", (o: RecoveryDecision) => { o.comparisons[0].relation = "greater"; }],
    ["false equality", (o: RecoveryDecision) => { o.comparisons[0].relation = "equal"; }],
    ["wrong comparison reference", (o: RecoveryDecision) => { o.comparisons[0].right.toRobotId = "R5"; }],
    ["self comparison", (o: RecoveryDecision) => { o.comparisons[0].right = { ...o.comparisons[0].left }; }],
    ["duplicate comparison", (o: RecoveryDecision) => { o.comparisons.push(structuredClone(o.comparisons[0])); }],
    ["omitted alternative", (o: RecoveryDecision) => { o.considered.pop(); }],
    ["duplicated alternative", (o: RecoveryDecision) => { o.considered[0] = structuredClone(o.considered[1]); }],
    ["missing peer comparison", (o: RecoveryDecision) => { o.comparisons = o.comparisons.filter(c => c.right.toRobotId !== "R5"); }],
    ["missing incident", (o: RecoveryDecision) => { o.incidents.pop(); }],
    ["invented incident severity", (o: RecoveryDecision) => { o.incidents[0].severity = "medium"; }],
    ["missing operator attention", (o: RecoveryDecision) => { o.operatorAttention.pop(); }],
    ["missing limitation", (o: RecoveryDecision) => { o.limitations.pop(); }],
    ["unjustified basis", (o: RecoveryDecision) => { if (o.decision.kind === "select") o.decision.basis = "tradeoff"; }],
  ] as const)("rejects %s independently of schema validity", (_name, mutate) => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4"); mutate(output);
    expect(() => validateRecoveryDecision(output, state, "minimize_pickup_steps")).toThrow("grounding");
    expect(() => explainRecoveryDecision(output, state, "minimize_pickup_steps")).toThrow("grounding");
  });
  it.each(["narrative", "approval", "executeImmediately", "predictedThroughput", "deadline", "energySavings"])("rejects unsupported conclusion field %s", field => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
    expect(() => validateRecoveryDecision({ ...output, [field]: "unverified claim" }, state, "minimize_pickup_steps")).toThrow("grounding");
    expect(() => validateRecoveryDecision({ ...output, decision: { ...output.decision, [field]: true } }, state, "minimize_pickup_steps")).toThrow("grounding");
  });
  it("rejects missing observation identity and invalid trusted candidate relationships", () => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
    delete state.observationId;
    expect(() => validateRecoveryDecision(output, state, "minimize_pickup_steps")).toThrow("grounding");
    const inconsistent = recoveryDecisionState(); inconsistent.jobs[0].assignedRobot = "R5";
    expect(() => validateRecoveryDecision(output, inconsistent, "minimize_pickup_steps")).toThrow("grounding");
  });
  it("rejects unknown metrics and fractional arithmetic", () => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
    output.comparisons[0].metric = "predictedThroughput" as DecisionMetric;
    expect(() => validateRecoveryDecision(output, state, "minimize_pickup_steps")).toThrow("grounding");
    output.comparisons[0].metric = "pickupSteps"; output.comparisons[0].leftValue = 1.5;
    expect(() => validateRecoveryDecision(output, state, "minimize_pickup_steps")).toThrow("grounding");
  });
});

describe("grounded abstention", () => {
  it.each(["none", "healthy", "poisoned", "tradeoff"] as const)("accepts appropriate %s abstention", mode => {
    const state = mode === "healthy" ? fleetFixture() : recoveryDecisionState(mode === "none" ? "none" : "tradeoff");
    if (mode === "poisoned") state.poisoned = true;
    const output = recoveryDecisionFixture(state, "compare_tradeoffs", null);
    expect(validateRecoveryDecision(output, state, "compare_tradeoffs")).toEqual(output);
  });
  it.each(["no_candidates", "no_supported_incident", "runtime_poisoned", "operator_tradeoff"] as const)("rejects false abstention %s when a single option attains both objectives", reason => {
    const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "compare_tradeoffs", null);
    output.decision = { kind: "abstain", reason };
    expect(() => validateRecoveryDecision(output, state, "compare_tradeoffs")).toThrow("grounding");
  });
  it("rejects recovery selection in a poisoned runtime", () => {
    const state = recoveryDecisionState(); state.poisoned = true;
    expect(() => validateRecoveryDecision(recoveryDecisionFixture(state, "compare_tradeoffs", "R4"), state, "compare_tradeoffs")).toThrow("grounding");
  });
});
