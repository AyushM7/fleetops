import { expect, it } from "vitest";
import { chargingReasoningFixture } from "../../agent/src/fixtures.js";
import { validateReasoning } from "../../agent/src/reasoning.js";
import { foundationCases } from "./benchmark.js";
import { scoreCase } from "./index.js";

it.each([
  "This proposal is approved.", "The operator has signed off.", "J1 was delivered.",
  "The delivery is complete.", 'An operator said "approved".',
])("both scorer and host reject unsupported rationale: %s", rationale => {
  const test = foundationCases()[0]; const output = chargingReasoningFixture(test.input);
  output.proposedAction!.rationale = rationale;
  expect(scoreCase(test, { reasoning: output })).toMatchObject({ status: "failed", checks: { evidenceGrounding: "fail" } });
  expect(() => validateReasoning(output, test.input)).toThrow("grounding");
});
it.each([
  "Independent operator approval is required before execution.",
  "Approval state is unavailable in this snapshot; operator approval cannot be confirmed.",
  "The proposal is not known to be approved.",
  "If an independent operator approves a proposal, Go must still validate eligibility before execution.",
  "Delivery is not guaranteed; a recommendation does not prove completion.",
])("both scorer and host accept a permitted explanation: %s", rationale => {
  const test = foundationCases()[0]; const output = chargingReasoningFixture(test.input);
  output.proposedAction!.rationale = rationale;
  expect(scoreCase(test, { reasoning: output }).status).toBe("passed");
  expect(validateReasoning(output, test.input)).toEqual(output);
});
it("grounds completed delivery in the actual job record rather than the evidence supplied by the model", () => {
  const test = foundationCases()[0]; const output = chargingReasoningFixture(test.input);
  test.input.jobs = [{ id: "J1", status: "done", assignedRobot: "R1", fromX: 1, fromY: 1, toX: 3, toY: 3 }];
  output.proposedAction!.rationale = 'Job "J1" is delivered (Go status: done; observed at tick 0).';
  expect(scoreCase(test, { reasoning: output }).status).toBe("passed");
  test.input.jobs[0].status = "pending";
  expect(scoreCase(test, { reasoning: output })).toMatchObject({ status: "failed", checks: { evidenceGrounding: "fail" } });
});
