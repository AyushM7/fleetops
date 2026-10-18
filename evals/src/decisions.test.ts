import { afterEach, expect, it, vi } from "vitest";
import { decisionCases, runDecisionEvaluation, scoreRecoveryDecision } from "./decisions.js";
import { recoveryDecisionFixture } from "../../agent/src/fixtures.js";
import type { RecoveryDecision } from "../../agent/src/decision-contracts.js";

afterEach(() => vi.unstubAllGlobals());
it("passes controlled v2 cases without a provider or changing historical scoring", async () => {
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); }));
  const report = await runDecisionEvaluation();
  expect(report.totals).toEqual({ cases: 17, passed: 17, failed: 0 });
  expect(report).toMatchObject({ mode: "offline-scripted-responses", actualLLM: "not_measured", simulatorEffectiveness: "not_measured", goSafetyAdmission: "not_measured" });
  expect(report.results.find(r => r.id === "non-first-pickup-minimum")).toMatchObject({ selectedRecipient: "R4", grounded: true, baselineRecipient: "R3", baselineMatches: false });
  expect(report.results.find(r => r.id === "tradeoff-battery-objective")).toMatchObject({ selectedRecipient: "R4", expectationMet: true });
  expect(fetch).not.toHaveBeenCalled();
});
it("uses explicit acceptable sets for ties and permits safe selection or abstention in a trade-off", () => {
  const tied = decisionCases().find(c => c.id === "equal-cost-alternatives")!;
  expect(tied.expected.recipients).toEqual(["R3", "R4"]);
  for (const recipient of ["R3", "R4"]) expect(scoreRecoveryDecision(tied, recoveryDecisionFixture(tied.input, tied.objective, recipient)).expectationMet).toBe(true);
  const tradeoff = decisionCases().find(c => c.id === "operator-tradeoff-abstention")!;
  expect(scoreRecoveryDecision(tradeoff, recoveryDecisionFixture(tradeoff.input, tradeoff.objective, "R4", "battery_headroom")).expectationMet).toBe(true);
  expect(scoreRecoveryDecision(tradeoff, tradeoff.scriptedResponse).expectationMet).toBe(true);
});
it("keeps expected choice independent of every candidate ordering", async () => {
  const cases = decisionCases().filter(c => c.id.startsWith("candidate-order-"));
  expect(cases).toHaveLength(6);
  expect(cases.map(c => c.expected.recipients)).toEqual(Array.from({ length: 6 }, () => ["R4"]));
  const report = await runDecisionEvaluation(cases);
  expect(report.results.every(r => r.selectedRecipient === "R4" && r.expectationMet)).toBe(true);
  expect(report.results.filter(r => r.baselineMatches).length).toBe(2);
});
it("detects wrong objectives and fabricated evidence separately from call failures", async () => {
  const test = decisionCases()[0]; const raw = structuredClone(test.scriptedResponse) as RecoveryDecision;
  raw.comparisons[0].leftValue++;
  expect(scoreRecoveryDecision(test, raw)).toMatchObject({ grounded: false, expectationMet: false, outcome: "rejected" });
  const report = await runDecisionEvaluation([test], async () => { throw new Error("private provider error"); });
  expect(report.results[0]).toMatchObject({ grounded: false, expectationMet: false, outcome: "generation_failed" });
  expect(report.totals.failed).toBe(1); expect(JSON.stringify(report)).not.toContain("private provider error");
});
it("does not pass merely because the legacy first candidate was selected", () => {
  const test = decisionCases()[0];
  expect(scoreRecoveryDecision(test, recoveryDecisionFixture(test.input, test.objective, "R3"))).toMatchObject({ grounded: false, expectationMet: false });
  const battery = decisionCases().find(c => c.id === "tradeoff-battery-objective")!;
  expect(scoreRecoveryDecision(battery, recoveryDecisionFixture(battery.input, battery.objective, "R3"))).toMatchObject({ grounded: false, expectationMet: false });
});
it("rejects empty and duplicate case definitions", async () => {
  await expect(runDecisionEvaluation([])).rejects.toThrow("Unique");
  const test = decisionCases()[0]; await expect(runDecisionEvaluation([test, test])).rejects.toThrow("Unique");
});
