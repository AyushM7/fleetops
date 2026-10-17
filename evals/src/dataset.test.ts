import { spawnSync } from "node:child_process";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { AIReasoning } from "../../agent/src/contracts.js";
import { reasoningSchema } from "../../agent/src/contracts.js";
import { validateSnapshot } from "../../agent/src/reasoning.js";
import { reasoningFixture, chargingReasoningFixture } from "../../agent/src/fixtures.js";
import { dataset, type Scenario } from "./dataset.js";
import { injectProbe, observerAdapter } from "./dataset-adapter.js";
import { metadataIssues, runDataset, summarizeDataset } from "./dataset-runner.js";
import { scoreCase } from "./index.js";

beforeEach(() => vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network authorized"); })));
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.unstubAllGlobals(); });

// Construct oracle answers from reviewer labels for testing the scorer, never for the evaluated adapter.
function referenceAnswer(test: Scenario): AIReasoning {
  const output = reasoningFixture(test.input); const action = test.expected.action; const state = test.input;
  if (!action) return output;
  if (action.action === "SetCharging") return { ...output, proposedAction: { ...action, rationale: "Human approval required.",
    evidence: { tick: state.tick, robot: { ...state.robots.find(r => r.id === action.robotId)! }, station: { ...state.stations.find(s => s.id === action.stationId)! } } } };
  return { ...output, proposedAction: { ...action, rationale: "Human approval required; delivery unmeasured.", evidence: {
    tick: state.tick, reference: { scope: "snapshot", tick: state.tick, robotId: action.fromRobotId, type: "traffic_blocked" },
    source: { ...state.robots.find(r => r.id === action.fromRobotId)! }, recipient: { ...state.robots.find(r => r.id === action.toRobotId)! },
    job: { ...state.jobs.find(j => j.id === action.jobId)! }, candidate: { ...state.reassignmentCandidates[0] } } } };
}

it("declares 16 unique scenarios with complete independently specified expectations", () => {
  const cases = dataset();
  expect(cases).toHaveLength(16); expect(new Set(cases.map(c => c.id)).size).toBe(16);
  expect(cases.filter(c => c.probe === "none")).toHaveLength(12);
  for (const test of cases) {
    expect(metadataIssues(test), test.id).toEqual([]);
    expect(() => validateSnapshot(test.input), test.id).not.toThrow();
    expect(reasoningSchema.safeParse(referenceAnswer(test)).success, test.id).toBe(true);
  }
});

it.each(dataset())("$id: correct grounded reasoning passes before any probe injection", test => {
  expect(scoreCase(test, { reasoning: referenceAnswer(test) }).status).toBe("passed");
});
it.each(dataset())("$id: incorrect action fails", test => {
  const output = referenceAnswer(test);
  if (!output.proposedAction) output.proposedAction = chargingReasoningFixture(test.input).proposedAction;
  else if (output.proposedAction.action === "SetCharging") output.proposedAction.stationId = "S999";
  else output.proposedAction.toRobotId = "R999";
  expect(scoreCase(test, { reasoning: output }).status).not.toBe("passed");
});
it.each(dataset())("$id: fabricated evidence fails", test => {
  const output = referenceAnswer(test);
  if (output.interpretations.length) output.interpretations[0].telemetry.battery = 1;
  else if (output.proposedAction?.action === "SetCharging") output.proposedAction.evidence.robot.battery = 1;
  else output.interpretations.push({ reference: { scope: "snapshot", tick: test.input.tick, robotId: "R999", type: "traffic_blocked" },
    severity: "high", telemetry: { ...test.input.robots[0] }, assessment: "Fabricated incident", recommendedResponse: "operator_attention" });
  expect(scoreCase(test, { reasoning: output }).checks.evidenceGrounding).toBe("fail");
});
it.each(dataset())("$id: unsupported command fails", test => {
  const output = referenceAnswer(test);
  expect(scoreCase(test, { reasoning: { ...output, proposedAction: { action: "RestoreRobot", robotId: "R1" } } }))
    .toMatchObject({ status: "malformed", checks: { unsupportedCommands: "fail" } });
});
it.each(dataset())("$id: missing required evidence fails", test => {
  const output = referenceAnswer(test);
  if (output.proposedAction) {
    const { evidence: _evidence, ...action } = output.proposedAction;
    expect(scoreCase(test, { reasoning: { ...output, proposedAction: action } }).status).toBe("malformed");
  } else if (output.interpretations.length) {
    const { telemetry: _telemetry, ...interpretation } = output.interpretations[0];
    expect(scoreCase(test, { reasoning: { ...output, interpretations: [interpretation] } }).status).toBe("malformed");
  } else {
    const { tick: _tick, ...noTick } = output;
    expect(scoreCase(test, { reasoning: noTick }).status).toBe("malformed");
  }
});
it.each(dataset().filter(c => c.expected.action === null))("$id: appropriate abstention accepted", test => {
  const result = scoreCase(test, { reasoning: referenceAnswer(test) });
  expect(result.status).toBe("passed"); expect(result.checks.appropriateAbstention).toBe("pass");
});
it.each(dataset().filter(c => c.probe !== "none"))("$id: exact expected rejection checks match", test => {
  const actual = scoreCase(test, { reasoning: injectProbe(referenceAnswer(test), test.probe) });
  expect({ status: actual.status, checks: actual.checks }).toEqual(test.expectedScoring);
});

it("exercises observer decisions without labels, preserves inputs, and reports lifecycle nonmeasurement", async () => {
  const cases = dataset(); const before = structuredClone(cases); const adapter = vi.fn(observerAdapter);
  const report = await runDataset(cases, adapter);
  expect(cases).toEqual(before);
  expect(adapter).toHaveBeenCalledTimes(16);
  expect(adapter.mock.calls[0]).toEqual([cases[0].input, cases[0].id]);
  expect(report.totals).toMatchObject({ decisions: 12, recommendationsPassed: 12, recommendationsUnsuccessful: 0,
    probes: 4, expectedRejections: 4, expectationMismatches: 0 });
  expect(report.evaluation.totals).toEqual({ cases: 16, passed: 12, unsuccessful: 4 });
  expect(report.scenarios.every(s => Object.values(s.actual.outcomes).every(o => o === "not_measured"))).toBe(true);
  for (const counts of Object.values(report.categories)) {
    for (const metric of Object.values(counts.metrics)) expect(metric.passed + metric.failed + metric.notApplicable).toBe(counts.decisions + counts.probes);
  }
  expect(summarizeDataset(report)).toContain("Probe rejections are not successful agent recommendations");
});
it("does not accept an always-abstaining adapter or count malformed/missing outputs as correct probes", async () => {
  const abstaining = await runDataset(dataset(), async input => ({ reasoning: reasoningFixture(input) }));
  expect(abstaining.totals.recommendationsUnsuccessful).toBeGreaterThan(0);
  const missing = await runDataset(dataset(), async () => ({ reasoning: undefined }));
  expect(missing.totals.recommendationsPassed).toBe(0); expect(missing.totals.expectedRejections).toBe(0);
  expect(missing.totals.expectationMismatches).toBe(16);
});
it("rejects incomplete metadata before calling the adapter and retains the case in counts", async () => {
  const test = dataset()[0]; test.requiredEvidence = []; const adapter = vi.fn(observerAdapter);
  const report = await runDataset([test], adapter);
  expect(adapter).not.toHaveBeenCalled(); expect(report.scenarios[0].actual.status).toBe("invalid_case");
  expect(report.totals).toMatchObject({ decisions: 1, recommendationsUnsuccessful: 1, expectationMismatches: 1 });
});
it("retains ambiguous/duplicate cases as unsuccessful rather than skipping them", async () => {
  const test = dataset()[0]; const adapter = vi.fn(observerAdapter);
  const report = await runDataset([test, structuredClone(test)], adapter);
  expect(adapter).not.toHaveBeenCalled(); expect(report.evaluation.totals).toEqual({ cases: 2, passed: 0, unsuccessful: 2 });
  expect(report.totals.expectationMismatches).toBe(2);
});
it.each(["missing target", "missing check", "decision expected to fail", "probe expected to pass"])("rejects invalid expectations: %s", async mode => {
  const test = dataset()[mode === "probe expected to pass" ? 12 : 0];
  if (mode === "missing target" && test.expected.action?.action === "ReassignUnstartedJob") test.expected.action.toRobotId = "";
  if (mode === "missing check") delete (test.expectedScoring.checks as Partial<typeof test.expectedScoring.checks>).evidenceGrounding;
  if (mode === "decision expected to fail") test.expectedScoring.status = "failed";
  if (mode === "probe expected to pass") test.expectedScoring.status = "passed";
  const adapter = vi.fn(observerAdapter); const report = await runDataset([test], adapter);
  expect(adapter).not.toHaveBeenCalled(); expect(report.totals.expectationMismatches).toBe(1);
  expect(report.scenarios[0].actual.status).toBe("invalid_case");
});
it("repeats the entire dataset and CLI with byte-identical JSON", async () => {
  expect(JSON.stringify(await runDataset(dataset()))).toBe(JSON.stringify(await runDataset(dataset())));
  const run = () => spawnSync(process.execPath, ["scripts/run.mjs"], { encoding: "utf8" });
  const first = run(); const second = run();
  expect(first.status).toBe(0); expect(second.status).toBe(0); expect(second.stdout).toBe(first.stdout);
  expect(JSON.parse(first.stdout).totals).toMatchObject({ decisions: 12, expectedRejections: 4, expectationMismatches: 0 });
  expect(first.stderr).toContain("Synthetic probes: 4/4 rejected as expected");
});
