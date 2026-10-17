import { Agent } from "@mastra/core/agent";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { runLive, runOffline, createFleetAgent } from "./agent.js";
import { createFleetTools } from "./tools.js";
import { validateReasoning, validateSnapshot } from "./reasoning.js";
import { createFleetClient } from "./graphql.js";
import { fleetFixture, pendingFixture, reasoningFixture } from "./fixtures.js";
import type { AIReasoning, FleetState } from "./contracts.js";

function recoverySnapshot(): FleetState {
  const state = fleetFixture(); state.tick = 42;
  state.robots[0] = { id: "R1", x: 0, y: 3, battery: 100, status: "moving", jobId: "J1", blocked: true, waitTicks: 42 };
  state.robots.push({ id: "R3", x: 4, y: 3, battery: 98, status: "idle", jobId: "", blocked: false, waitTicks: 0 });
  state.activeIncidents = [{ robotId: "R1", type: "traffic_blocked", severity: "high", telemetry: { ...state.robots[0] } }];
  state.jobs = [{ id: "J1", status: "assigned", assignedRobot: "R1", fromX: 4, fromY: 4, toX: 6, toY: 4 }];
  state.reassignmentCandidates = [{ jobId: "J1", fromRobotId: "R1", toRobotId: "R3", pickupSteps: 1, deliverySteps: 2, chargingExitSteps: 5, requiredBattery: 13 }];
  return state;
}
function recoveryReasoning(state = recoverySnapshot()): AIReasoning {
  return { ...reasoningFixture(state), proposedAction: { action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: "R3",
    purpose: "incident_recovery", rationale: "Transfer may enable job progress; the obstruction remains.", evidence: { tick: state.tick,
      reference: { scope: "snapshot", tick: state.tick, robotId: "R1", type: "traffic_blocked" },
      source: { ...state.robots[0] }, recipient: { ...state.robots[1] }, job: { ...state.jobs[0] }, candidate: { ...state.reassignmentCandidates[0] } } } };
}
function apiFixture(state = recoverySnapshot()) {
  return { readFleet: vi.fn().mockImplementation(async () => structuredClone(state)),
    submitCharging: vi.fn().mockResolvedValue(pendingFixture), submitReassignment: vi.fn().mockResolvedValue(pendingFixture) };
}
beforeEach(() => { vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); })); });
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.restoreAllMocks(); vi.unstubAllEnvs(); vi.unstubAllGlobals(); });

it("uses Go candidate evidence deterministically; submission defaults off and Mastra has only the read tool", async () => {
  const api = apiFixture(); const first = await runOffline(api);
  expect(await runOffline(api)).toEqual(first);
  expect(first.recommendedAction).toEqual({ action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: "R3" });
  expect(first.proposal.status).toBe("disabled"); expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(Object.keys(await createFleetAgent(api, "openai/test").agent.listTools())).toEqual(["readFleetState"]);
});
it("rechecks current facts and submits exactly one pending proposal after explicit opt-in", async () => {
  const api = apiFixture(); const result = await runOffline(api, { allowSubmit: true });
  expect(result.proposal.status).toBe("pending_approval"); expect(result.submissionCheck).toBe("current");
  expect(api.readFleet).toHaveBeenCalledTimes(2); expect(api.submitReassignment).toHaveBeenCalledTimes(1); expect(api.submitCharging).not.toHaveBeenCalled();
  const session = createFleetTools(api, true); const input = { jobId: "J1", fromRobotId: "R1", toRobotId: "R3", expiresAt: "2026-10-16T12:01:00Z" };
  await Promise.all([session.submit(input), session.submit(input)]); expect(api.submitReassignment).toHaveBeenCalledTimes(2);
});
it.each(["tick", "eligibility", "unavailable"])("does not submit when fresh state changes: %s", async mode => {
  const api = apiFixture(); const fresh = recoverySnapshot();
  if (mode === "tick") fresh.tick++;
  if (mode === "eligibility") { fresh.reassignmentCandidates = []; fresh.robots[1].battery = 29; }
  api.readFleet.mockResolvedValueOnce(recoverySnapshot());
  if (mode === "unavailable") api.readFleet.mockRejectedValueOnce(new Error("private backend error")); else api.readFleet.mockResolvedValueOnce(fresh);
  const result = await runOffline(api, { allowSubmit: true });
  expect(result.recommendedAction).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["invented job", "wrong recipient", "old reference", "invented route", "wrong telemetry", "unsupported command"])("rejects ungrounded model action: %s", async mode => {
  const reasoning = recoveryReasoning(); const action = reasoning.proposedAction;
  if (action?.action !== "ReassignUnstartedJob") throw new Error("fixture");
  if (mode === "invented job") action.jobId = "J999";
  if (mode === "wrong recipient") action.toRobotId = "R4";
  if (mode === "old reference") action.evidence.reference.tick--;
  if (mode === "invented route") action.evidence.candidate.pickupSteps++;
  if (mode === "wrong telemetry") action.evidence.recipient.battery--;
  const untrusted = mode === "unsupported command" ? { ...reasoning, proposedAction: { ...action, action: "MoveRobot" } } : reasoning;
  expect(() => validateReasoning(untrusted, recoverySnapshot())).toThrow();
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key"); vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: untrusted } as never);
  const api = apiFixture(); expect((await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true })).aiAnalysis.status).toBe("rejected");
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("accepts grounded AI reasoning and gates host submission", async () => {
  const state = recoverySnapshot(); expect(validateReasoning(recoveryReasoning(state), state)).toEqual(recoveryReasoning(state));
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key"); vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryReasoning() } as never);
  const api = apiFixture(); const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("accepted"); expect(result.proposal.status).toBe("pending_approval");
});
it("rejects ambiguous or inconsistent backend candidate evidence", () => {
  const duplicate = recoverySnapshot(); duplicate.reassignmentCandidates.push({ ...duplicate.reassignmentCandidates[0] }); expect(() => validateSnapshot(duplicate)).toThrow();
  const absent = recoverySnapshot(); absent.activeIncidents = []; expect(() => validateSnapshot(absent)).toThrow();
  const picked = recoverySnapshot(); picked.jobs[0].status = "in_progress"; expect(() => validateSnapshot(picked)).toThrow();
});
it("does not retry an uncertain submission or claim execution", async () => {
  const api = apiFixture(); api.submitReassignment.mockRejectedValue(new Error("private transport error"));
  const result = await runOffline(api, { allowSubmit: true }); expect(result.proposal.status).toBe("unknown"); expect(api.submitReassignment).toHaveBeenCalledTimes(1);
});
it("uses only the typed GraphQL submission operation", async () => {
  const transport = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { submitReassignment: pendingFixture } }), { status: 200 }));
  const client = createFleetClient("http://127.0.0.1/graphql", "a".repeat(32), transport);
  expect(await client.submitReassignment({ jobId: "J1", fromRobotId: "R1", toRobotId: "R3", expiresAt: "2026-10-16T12:01:00Z" })).toEqual(pendingFixture);
  const body = JSON.parse(transport.mock.calls[0][1].body); expect(body.query).toContain("submitReassignment"); expect(body.query).not.toMatch(/approve|reject|tick|inject/i);
});
