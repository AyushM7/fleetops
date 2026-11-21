import { describe, expect, it, vi } from "vitest";
import { offlineModelFixture } from "./fixtures.js";
import { makeOperationsDecision } from "./operations-manager.js";

const signal = () => AbortSignal.timeout(1_000);

describe("Operations Manager with a mocked model", () => {
  it("produces a structured assignment in one model invocation", async () => {
    const onCall = vi.fn();
    const decision = { kind: "assign", assignments: [{ jobId: "J1", robotId: "R10" }] };
    const result = await makeOperationsDecision('{"tick":1}', offlineModelFixture({ decision }, onCall), signal());
    expect(result.decision).toEqual(decision);
    expect(result.usage).toBeUndefined();
    expect(onCall).toHaveBeenCalledTimes(1);
  });

  it.each([
    { kind: "assign", assignments: [{ jobId: "J1", robotId: "R11" }] },
    { kind: "assign", assignments: [{ jobId: "J1", robotId: "M1" }] },
    { kind: "reassign", reassignment: { jobId: "J1", fromRobotId: "M1", toRobotId: "R2" } },
    { kind: "reassign", reassignment: { jobId: "J1", fromRobotId: "R2", toRobotId: "M1" } },
    { kind: "fault", robotId: "R2" },
    { kind: "abstain", reason: "no_safe_option", runtimeId: "other" },
  ])("rejects unsafe or out-of-scope output %j without retrying", async decision => {
    const onCall = vi.fn();
    const result = await makeOperationsDecision("{}", offlineModelFixture({ decision }, onCall), signal());
    expect(result.decision).toBeUndefined();
    expect(result.failure).toBeDefined();
    expect(onCall).toHaveBeenCalledTimes(1);
  });

  it("does not call the model on oversized input or cancellation", async () => {
    const onCall = vi.fn();
    const model = offlineModelFixture({}, onCall);
    expect(await makeOperationsDecision("X".repeat(50_000), model, signal())).toEqual({ failure: "model_unavailable" });
    expect(await makeOperationsDecision("{}", model, AbortSignal.abort())).toEqual({ failure: "model_unavailable" });
    expect(onCall).not.toHaveBeenCalled();
  });
});
