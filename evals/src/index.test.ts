import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { spawnSync } from "node:child_process";
import { chargingReasoningFixture, reasoningFixture } from "../../agent/src/fixtures.js";
import { foundationCases, foundationDouble } from "./benchmark.js";
import { runEvaluation, scoreCase, summarize } from "./index.js";

beforeEach(() => vi.stubGlobal("fetch", vi.fn(() => { throw new Error("Network forbidden"); })));
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.unstubAllGlobals(); });

it("scores a grounded supported recommendation against independently specified labels", () => {
  const test = foundationCases()[0];
  const result = scoreCase(test, { reasoning: chargingReasoningFixture(test.input) });
  expect(result.status).toBe("passed");
  expect(result.checks).toEqual({ incidentReferences: "pass", evidenceGrounding: "pass", recommendationValidity: "pass",
    unsupportedCommands: "pass", operatorAttention: "not_applicable", appropriateAbstention: "not_applicable" });
});
it("recognizes operator attention with appropriate abstention", () => {
  const test = foundationCases()[1];
  expect(scoreCase(test, { reasoning: reasoningFixture(test.input) })).toMatchObject({ status: "passed",
    checks: { operatorAttention: "pass", appropriateAbstention: "pass" } });
});
it.each(["fabricated", "stale", "duplicate", "omitted"])("rejects %s incident references", mode => {
  const test = foundationCases()[1];
  const output = reasoningFixture(test.input);
  if (mode === "fabricated") output.interpretations[0].reference.robotId = "R999";
  if (mode === "stale") output.interpretations[0].reference.tick--;
  if (mode === "duplicate") output.interpretations.push(structuredClone(output.interpretations[0]));
  if (mode === "omitted") output.interpretations = [];
  expect(scoreCase(test, { reasoning: output })).toMatchObject({ status: "failed",
    checks: { incidentReferences: "fail", appropriateAbstention: "fail" } });
});
it.each(["telemetry", "severity", "action evidence", "wrong target"])("rejects altered %s", mode => {
  const test = foundationCases()[mode === "telemetry" || mode === "severity" ? 1 : 0];
  const output = test.expected.action ? chargingReasoningFixture(test.input) : reasoningFixture(test.input);
  if (mode === "telemetry") output.interpretations[0].telemetry.battery++;
  if (mode === "severity") output.interpretations[0].severity = "medium";
  if (output.proposedAction?.action === "SetCharging") {
    if (mode === "action evidence") output.proposedAction.evidence.robot.battery++;
    if (mode === "wrong target") output.proposedAction.stationId = "S999";
  }
  expect(scoreCase(test, { reasoning: output })).toMatchObject({ status: "failed", checks: { evidenceGrounding: "fail" } });
});
it.each(["MoveRobot", "Tick", "ApproveProposal", "Driver", "Commander"])("rejects unsupported %s commands", action => {
  const test = foundationCases()[0];
  const output = chargingReasoningFixture(test.input);
  expect(scoreCase(test, { reasoning: { ...output, proposedAction: { ...output.proposedAction, action } } }))
    .toMatchObject({ status: "malformed", checks: { unsupportedCommands: "fail" } });
});
it("fails a supported command when ground truth requires abstention", () => {
  const test = foundationCases()[1];
  expect(scoreCase(test, { reasoning: chargingReasoningFixture(test.input) })).toMatchObject({ status: "failed",
    checks: { unsupportedCommands: "pass", recommendationValidity: "fail", appropriateAbstention: "fail" } });
});
it("fails abstention when an action is required", () => {
  const test = foundationCases()[0];
  expect(scoreCase(test, { reasoning: reasoningFixture(test.input) })).toMatchObject({ status: "failed",
    checks: { recommendationValidity: "fail" } });
});
it.each([undefined, null])("reports missing output (%s) without a success", reasoning => {
  expect(scoreCase(foundationCases()[1], { reasoning }).status).toBe("missing");
});
it.each([{}, "not JSON", [], { tick: 0 }, { tick: 42, interpretations: [], proposedAction: null, executed: true }])(
  "reports malformed output without a success: %j", reasoning => {
    expect(scoreCase(foundationCases()[1], { reasoning }).status).toBe("malformed");
  });
it("requires operator_attention rather than another response", () => {
  const test = foundationCases()[1]; const output = reasoningFixture(test.input);
  expect(scoreCase(test, { reasoning: { ...output, interpretations: [{ ...output.interpretations[0], recommendedResponse: "ignore" }] } }))
    .toMatchObject({ status: "malformed", checks: { operatorAttention: "fail" } });
});
it("reports ambiguous inputs and duplicate case IDs before invoking the adapter", async () => {
  const test = foundationCases()[1]; test.input.activeIncidents.push(structuredClone(test.input.activeIncidents[0]));
  const adapter = vi.fn(foundationDouble);
  const cases = foundationCases();
  const report = await runEvaluation([test, cases[0], structuredClone(cases[0])], adapter);
  expect(report.results.map(r => r.status)).toEqual(["invalid_case", "invalid_case", "invalid_case"]);
  expect(adapter).not.toHaveBeenCalled();
});
it("rejects expected references inconsistent with supplied Go classifications", async () => {
  const test = foundationCases()[1]; test.expected.incidents[0].type = "traffic_blocked";
  expect((await runEvaluation([test], foundationDouble)).results[0].status).toBe("invalid_case");
});
it("reports adapter errors without leaking error strings or treating them as abstention", async () => {
  const report = await runEvaluation(foundationCases(), async () => { throw new Error("private transport detail"); });
  expect(report.totals).toEqual({ cases: 2, passed: 0, unsuccessful: 2 });
  expect(report.results.every(r => r.status === "adapter_error")).toBe(true);
  expect(JSON.stringify(report)).not.toContain("private transport detail");
});
it("preserves inputs/labels and produces byte-stable JSON with no API or mutation capabilities", async () => {
  const cases = foundationCases(); const before = structuredClone(cases);
  const adapter = vi.fn(foundationDouble);
  const first = await runEvaluation(cases, adapter);
  expect(JSON.stringify(await runEvaluation(cases, adapter))).toBe(JSON.stringify(first));
  expect(cases).toEqual(before);
  expect(adapter.mock.calls[0]).toEqual([cases[0].input, cases[0].id]);
  expect(first.totals).toEqual({ cases: 2, passed: 2, unsuccessful: 0 });
  expect(first.metrics.operatorAttention).toEqual({ passed: 1, failed: 0, notApplicable: 1 });
  expect(first.results.every(r => Object.values(r.outcomes).every(v => v === "not_measured"))).toBe(true);
  expect(summarize(first)).toContain("Live LLM accuracy: not measured");
});
it("isolates adapter snapshot mutation from the case and scorer", async () => {
  const cases = foundationCases(); const before = structuredClone(cases);
  const report = await runEvaluation(cases, async input => {
    input.tick++; return { reasoning: reasoningFixture(input) };
  });
  expect(cases).toEqual(before); expect(report.totals.passed).toBe(0);
});
it("emits stable machine JSON separately from the CLI summary with exit zero", () => {
  const run = () => spawnSync(process.execPath, ["scripts/run.mjs", "--foundation"], { encoding: "utf8" });
  const first = run(); const second = run();
  expect(first.status).toBe(0); expect(second.status).toBe(0);
  expect(second.stdout).toBe(first.stdout);
  expect(JSON.parse(first.stdout).totals).toEqual({ cases: 2, passed: 2, unsuccessful: 0 });
  expect(first.stderr).toContain("Offline deterministic contract checks: 2/2 passed");
});
it("scores Go-admitted reassignment evidence and rejects an invented candidate", () => {
  // Mirrors agent/src/reassignment.test.ts recoverySnapshot; no delivery claim.
  const test = foundationCases()[0]; const state = test.input;
  state.tick = 42;
  state.robots[0] = { id: "R1", x: 0, y: 3, battery: 100, status: "moving", jobId: "J1", blocked: true, waitTicks: 42 };
  state.robots.push({ id: "R3", x: 4, y: 3, battery: 98, status: "idle", jobId: "", blocked: false, waitTicks: 0 });
  state.activeIncidents = [{ robotId: "R1", type: "traffic_blocked", severity: "high", telemetry: { ...state.robots[0] } }];
  state.jobs = [{ id: "J1", status: "assigned", assignedRobot: "R1", fromX: 4, fromY: 4, toX: 6, toY: 4 }];
  state.reassignmentCandidates = [{ jobId: "J1", fromRobotId: "R1", toRobotId: "R3", pickupSteps: 1, deliverySteps: 2, chargingExitSteps: 5, requiredBattery: 13 }];
  test.expected = { incidents: [{ scope: "snapshot", tick: 42, robotId: "R1", type: "traffic_blocked" }],
    action: { action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: "R3", purpose: "incident_recovery" } };
  const output = { ...reasoningFixture(state), proposedAction: { action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: "R3",
    purpose: "incident_recovery", rationale: "May recover progress after approval; obstruction remains.", evidence: { tick: 42,
      reference: test.expected.incidents[0], source: { ...state.robots[0] }, recipient: { ...state.robots[1] },
      job: { ...state.jobs[0] }, candidate: { ...state.reassignmentCandidates[0] } } } };
  expect(scoreCase(test, { reasoning: output }).status).toBe("passed");
  output.proposedAction.evidence.candidate.pickupSteps++;
  expect(scoreCase(test, { reasoning: output })).toMatchObject({ status: "failed", checks: { evidenceGrounding: "fail" } });
});
