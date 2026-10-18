import { expect, it, vi } from "vitest";
import { completionRecord, finishDecisionRun } from "./agent-run.js";
import { createFleetClient } from "./graphql.js";
import { fleetFixture, pendingFixture, recoveryDecisionFixture, recoveryDecisionState } from "./fixtures.js";
import type { InvestigationTrace } from "./investigation-budget.js";

function trace(): InvestigationTrace { return { requests: 2, toolCalls: 1, inputTokens: 10, outputTokens: 5, totalTokens: 15,
  usageReports: 1, events: [{ kind: "request", name: "untrusted raw model" }, { kind: "tool", name: "readRobots", input: { credential: "private-secret" }, output: { prompt: "private-secret" } }] }; }
it("projects bounded activity and decision claims without raw trace data or invented measurements", () => {
  const state = recoveryDecisionState(), decision = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  const record = completionRecord("accepted", decision, trace(), 12.4);
  expect(record.reportedUsage).toEqual({ status: "partial", inputTokens: 10, outputTokens: 5, totalTokens: 15 });
  expect(record.reportedActualModel).toBe(""); expect(record.latencyMs).toBe(12);
  expect(record.steps).toEqual([{ kind: "request", tool: "" }, { kind: "tool", tool: "readRobots" }]);
  expect(record.decision?.selected).toEqual({ jobId: "J1", fromRobotId: "R1", toRobotId: "R4" });
  expect(JSON.stringify(record)).not.toMatch(/private-secret|untrusted raw model|recipient|telemetry|credential|prompt/);
  expect(record).not.toHaveProperty("estimatedCost");
  const missing = trace(); missing.usageReports = 0;
  expect(completionRecord("failed", null, missing, 1).reportedUsage).toEqual({ status: "unavailable", inputTokens: null, outputTokens: null, totalTokens: null });
});
it.each(["failed", "rejected"] as const)("persists %s without proposal submission", async status => {
  const state = fleetFixture(), handle = { id: "Arun", runtimeId: "runtime", observationId: state.observationId! };
  const api = { readFleet: vi.fn().mockResolvedValue(state), submitCharging: vi.fn(), submitReassignment: vi.fn(),
    finishAgentRun: vi.fn().mockResolvedValue({ runId: handle.id, recorded: true, denial: "", proposalResult: null }) };
  const result = await finishDecisionRun(api, handle, state, completionRecord(status, null, trace(), 1), null, true);
  expect(api.finishAgentRun).toHaveBeenCalledTimes(1); expect(api.finishAgentRun.mock.calls[0][1].submit).toBeNull();
  expect(result.provenance.status).toBe("recorded"); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["acknowledged", "unknown"])("uses only the atomic completion endpoint for a selected proposal: %s", async outcome => {
  const state = recoveryDecisionState(), handle = { id: "Arun", runtimeId: "runtime", observationId: state.observationId! };
  const decision = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  const finish = vi.fn(); if (outcome === "unknown") finish.mockRejectedValue(new Error("private-secret"));
  else finish.mockResolvedValue({ runId: handle.id, recorded: true, denial: "", proposalResult: pendingFixture });
  const api = { readFleet: vi.fn().mockResolvedValue(state), submitCharging: vi.fn(), submitReassignment: vi.fn(), finishAgentRun: finish };
  const result = await finishDecisionRun(api, handle, state, completionRecord("accepted", decision, trace(), 1), { action: "ReassignUnstartedJob", ...decision.considered[1].candidate }, true);
  expect(finish).toHaveBeenCalledTimes(1); expect(api.submitCharging).not.toHaveBeenCalled(); expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(finish.mock.calls[0][1]).toMatchObject({ runtimeId: "runtime", observationId: state.observationId, submit: { expiresAt: expect.any(String) } });
  expect(result.provenance.status).toBe(outcome === "unknown" ? "unknown" : "recorded");
  expect(result.proposal.status).toBe(outcome === "unknown" ? "unknown" : "pending_approval");
  expect(JSON.stringify(result)).not.toContain("private-secret");
});
it("uses the authenticated GraphQL protocol and rejects misleading completion receipts", async () => {
  const state = fleetFixture(), handle = { id: "Arun", runtimeId: "runtime", observationId: state.observationId! };
  const transport = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: { startAgentRun: handle } }), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ data: { finishAgentRun: { runId: "Arun", recorded: false, denial: "", proposalResult: null } } }), { status: 200 }));
  const api = createFleetClient("http://127.0.0.1/graphql", "a".repeat(32), transport);
  expect(await api.startAgentRun!(state.observationId!, { version: "agent-run-v1", contract: "recovery-decision-v2", objective: "compare_tradeoffs", reportedSource: "mocked", requestedModel: "offline/test" })).toEqual(handle);
  await expect(api.finishAgentRun!(handle.id, { runtimeId: "runtime", observationId: state.observationId!, completion: completionRecord("failed", null, trace(), 1), submit: null })).rejects.toThrow("malformed");
  expect(transport).toHaveBeenCalledTimes(2);
});
