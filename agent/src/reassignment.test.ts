import { Agent } from "@mastra/core/agent";
import { readFileSync } from "node:fs";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { runLive, runOffline, createFleetAgent } from "./agent.js";
import { createFleetTools } from "./tools.js";
import { validateReasoning, validateSnapshot } from "./reasoning.js";
import { createFleetClient } from "./graphql.js";
import { pendingFixture, reasoningFixture } from "./fixtures.js";
import type { AIReasoning, FleetState } from "./contracts.js";

const observations = JSON.parse(readFileSync(new URL("../../docs/evidence/demo-08/observations.json", import.meta.url), "utf8")) as
  { id: string; snapshot: unknown }[];
type RecoveryAction = Extract<NonNullable<AIReasoning["proposedAction"]>, { action: "ReassignUnstartedJob" }>;

function recoverySnapshot(): FleetState {
  return { ...validateSnapshot(observations.find(o => o.id === "blocked-reassignment")?.snapshot), observationId: "a".repeat(64) };
}
function recoveryReasoning(state = recoverySnapshot(), toRobotId = "R3"): AIReasoning & { proposedAction: RecoveryAction } {
  const candidate = state.reassignmentCandidates.find(c => c.toRobotId === toRobotId);
  const source = state.robots.find(r => r.id === candidate?.fromRobotId);
  const recipient = state.robots.find(r => r.id === candidate?.toRobotId);
  const job = state.jobs.find(j => j.id === candidate?.jobId);
  if (!candidate || !source || !recipient || !job) throw new Error("Missing Go recovery fixture evidence.");
  return { ...reasoningFixture(state), proposedAction: { action: "ReassignUnstartedJob", jobId: job.id, fromRobotId: source.id, toRobotId: recipient.id,
    purpose: "incident_recovery", rationale: "Transfer may enable job progress; the obstruction remains.", evidence: { tick: state.tick,
      reference: { scope: "snapshot", tick: state.tick, robotId: "R1", type: "traffic_blocked" },
      source: { ...source }, recipient: { ...recipient }, job: { ...job }, candidate: { ...candidate } } } };
}
function apiFixture(state = recoverySnapshot()) {
  return { readFleet: vi.fn().mockImplementation(async () => structuredClone(state)),
    submitCharging: vi.fn().mockResolvedValue(pendingFixture), submitReassignment: vi.fn().mockResolvedValue(pendingFixture) };
}
beforeEach(() => { vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); })); });
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.restoreAllMocks(); vi.unstubAllEnvs(); vi.unstubAllGlobals(); });

it("uses Go candidate evidence deterministically; submission defaults off and Mastra has only the read tool", async () => {
  const api = apiFixture(); const first = await runOffline(api);
  expect(recoverySnapshot().reassignmentCandidates.map(c => c.toRobotId)).toEqual(["R3", "R4", "R5"]);
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
  if (mode === "eligibility") { fresh.reassignmentCandidates = []; fresh.robots.find(r => r.id === "R3")!.battery = 29; }
  api.readFleet.mockResolvedValueOnce(recoverySnapshot());
  if (mode === "unavailable") api.readFleet.mockRejectedValueOnce(new Error("private backend error")); else api.readFleet.mockResolvedValueOnce(fresh);
  const result = await runOffline(api, { allowSubmit: true });
  expect(result.recommendedAction).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each([
  ["invented job", (a: RecoveryAction) => { a.jobId = "J999"; }],
  ["fabricated recipient", (a: RecoveryAction) => { a.toRobotId = "R999"; }],
  ["wrong source", (a: RecoveryAction) => { a.fromRobotId = "R3"; }],
  ["mismatched admitted recipient", (a: RecoveryAction) => { a.toRobotId = "R5"; }],
  ["old evidence tick", (a: RecoveryAction) => { a.evidence.tick--; }],
  ["old incident reference", (a: RecoveryAction) => { a.evidence.reference.tick--; }],
  ["wrong incident owner", (a: RecoveryAction) => { a.evidence.reference.robotId = "R2"; }],
  ["wrong incident type", (a: RecoveryAction) => { a.evidence.reference.type = "robot_unresponsive"; }],
  ["invented pickup route", (a: RecoveryAction) => { a.evidence.candidate.pickupSteps++; }],
  ["invented delivery route", (a: RecoveryAction) => { a.evidence.candidate.deliverySteps++; }],
  ["invented charging exit", (a: RecoveryAction) => { a.evidence.candidate.chargingExitSteps++; }],
  ["invented battery requirement", (a: RecoveryAction) => { a.evidence.candidate.requiredBattery++; }],
  ["fabricated candidate tuple", (a: RecoveryAction) => { a.evidence.candidate.toRobotId = "R999"; }],
  ["wrong source telemetry", (a: RecoveryAction) => { a.evidence.source.battery--; }],
  ["wrong recipient telemetry", (a: RecoveryAction) => { a.evidence.recipient.battery--; }],
  ["wrong job ownership", (a: RecoveryAction) => { a.evidence.job.assignedRobot = "R5"; }],
  ["wrong job status", (a: RecoveryAction) => { a.evidence.job.status = "in_progress"; }],
] as const)("rejects ungrounded non-first model action: %s", async (_mode, change) => {
  const untrusted = recoveryReasoning(recoverySnapshot(), "R4"); change(untrusted.proposedAction);
  expect(() => validateReasoning(untrusted, recoverySnapshot())).toThrow();
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key"); vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: untrusted } as never);
  const api = apiFixture(); expect((await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true })).aiAnalysis.status).toBe("rejected");
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["R3", "R4", "R5"])("accepts grounded %s reasoning and submits that exact candidate with a mocked model", async recipient => {
  const state = recoverySnapshot(); const reasoning = recoveryReasoning(state, recipient);
  expect(validateReasoning(reasoning, state)).toEqual(reasoning);
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key");
  const generate = vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: reasoning } as never);
  const api = apiFixture(); const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("accepted"); expect(result.proposal.status).toBe("pending_approval");
  expect(result.submissionCheck).toBe("current");
  expect(result.recommendedAction).toEqual({ action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: recipient });
  expect(api.submitReassignment).toHaveBeenCalledExactlyOnceWith({ jobId: "J1", fromRobotId: "R1", toRobotId: recipient, expiresAt: expect.any(String), expectedObservationId: state.observationId });
  expect(api.readFleet).toHaveBeenCalledTimes(2); expect(generate).toHaveBeenCalledTimes(1);
  expect(api.submitCharging).not.toHaveBeenCalled();
});
it("rejects an unsupported command instead of converting it into a proposal", async () => {
  const reasoning = recoveryReasoning(recoverySnapshot(), "R4");
  const untrusted = { ...reasoning, proposedAction: { ...reasoning.proposedAction, action: "MoveRobot" } };
  expect(() => validateReasoning(untrusted, recoverySnapshot())).toThrow("grounding");
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key");
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: untrusted } as never);
  const api = apiFixture();
  expect((await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true })).aiAnalysis.status).toBe("rejected");
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["R3", "R4", "R5"])("admits %s through the submission tool's fresh candidate membership check", async recipient => {
  const api = apiFixture(); const session = createFleetTools(api, true);
  const input = { jobId: "J1", fromRobotId: "R1", toRobotId: recipient, expiresAt: "2026-10-16T12:01:00Z" };
  expect(await session.submit(input)).toEqual({ status: "pending_approval", proposalId: pendingFixture.proposalId });
  expect(api.readFleet).toHaveBeenCalledTimes(1);
  expect(api.submitReassignment).toHaveBeenCalledExactlyOnceWith({ ...input, expectedObservationId: recoverySnapshot().observationId });
});
it.each(["removed", "fabricated", "wrong job", "wrong source", "poisoned", "unavailable"])("blocks reassignment at the submission boundary: %s", async mode => {
  const state = recoverySnapshot(); const api = apiFixture(state);
  const input = { jobId: "J1", fromRobotId: "R1", toRobotId: "R4", expiresAt: "2026-10-16T12:01:00Z" };
  if (mode === "removed") state.reassignmentCandidates = state.reassignmentCandidates.filter(c => c.toRobotId !== "R4");
  if (mode === "fabricated") input.toRobotId = "R999";
  if (mode === "wrong job") input.jobId = "J999";
  if (mode === "wrong source") input.fromRobotId = "R3";
  if (mode === "poisoned") state.poisoned = true;
  if (mode === "unavailable") api.readFleet.mockRejectedValue(new Error("private read error"));
  const session = createFleetTools(api, true);
  expect(await session.submit(input)).toEqual({ status: "not_requested" });
  expect(await session.submit(input)).toEqual({ status: "not_requested" });
  expect(api.readFleet).toHaveBeenCalledTimes(1);
  expect(api.submitReassignment).not.toHaveBeenCalled(); expect(api.submitCharging).not.toHaveBeenCalled();
});
it.each(["tick", "candidate removed", "candidate metrics", "telemetry", "unavailable"])("does not submit a non-first model choice after changed or unavailable facts: %s", async mode => {
  const state = recoverySnapshot(); const fresh = recoverySnapshot(); const api = apiFixture(state);
  if (mode === "tick") fresh.tick++;
  if (mode === "candidate removed") fresh.reassignmentCandidates = fresh.reassignmentCandidates.filter(c => c.toRobotId !== "R4");
  if (mode === "candidate metrics") fresh.reassignmentCandidates.find(c => c.toRobotId === "R4")!.pickupSteps++;
  if (mode === "telemetry") fresh.robots.find(r => r.id === "R4")!.battery--;
  api.readFleet.mockResolvedValueOnce(state);
  if (mode === "unavailable") api.readFleet.mockRejectedValueOnce(new Error("private read error"));
  else api.readFleet.mockResolvedValueOnce(fresh);
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key");
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryReasoning(state, "R4") } as never);
  const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("accepted");
  expect(result.submissionCheck).toBe(mode === "unavailable" ? "unavailable" : "snapshot_changed");
  expect(result.recommendedAction).toBeNull(); expect(result.proposal.status).toBe("not_requested");
  expect(api.readFleet).toHaveBeenCalledTimes(2);
  expect(api.submitReassignment).not.toHaveBeenCalled(); expect(api.submitCharging).not.toHaveBeenCalled();
});
it.each(["poisoned", "demo"])("preserves the reassignment prohibition for %s runs", async mode => {
  const state = recoverySnapshot(); if (mode === "poisoned") state.poisoned = true;
  const demo = mode === "demo" ? { robotId: "R4", stationId: "S1" } : undefined;
  expect(() => validateReasoning(recoveryReasoning(state, "R4"), state, demo)).toThrow("grounding");
  const api = apiFixture(state); const session = createFleetTools(api, true, demo);
  await session.submit({ jobId: "J1", fromRobotId: "R1", toRobotId: "R4", expiresAt: "2026-10-16T12:01:00Z" });
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("rejects ambiguous or inconsistent backend candidate evidence", () => {
  const duplicate = recoverySnapshot(); duplicate.reassignmentCandidates.push({ ...duplicate.reassignmentCandidates[0] }); expect(() => validateSnapshot(duplicate)).toThrow();
  const absent = recoverySnapshot(); absent.activeIncidents = []; expect(() => validateSnapshot(absent)).toThrow();
  const picked = recoverySnapshot(); picked.jobs[0].status = "in_progress"; expect(() => validateSnapshot(picked)).toThrow();
  const owner = recoverySnapshot(); owner.jobs[0].assignedRobot = "R5"; expect(() => validateSnapshot(owner)).toThrow();
});
it("does not retry an uncertain submission or claim execution", async () => {
  const api = apiFixture(); api.submitReassignment.mockRejectedValue(new Error("private transport error"));
  const result = await runOffline(api, { allowSubmit: true }); expect(result.proposal.status).toBe("unknown"); expect(api.submitReassignment).toHaveBeenCalledTimes(1);
});
it("does not retry an uncertain non-first submission, including concurrent and later calls", async () => {
  const api = apiFixture(); api.submitReassignment.mockRejectedValue(new Error("private transport error"));
  const session = createFleetTools(api, true);
  const input = { jobId: "J1", fromRobotId: "R1", toRobotId: "R5", expiresAt: "2026-10-16T12:01:00Z" };
  expect(await Promise.all([session.submit(input), session.submit(input)])).toEqual([{ status: "unknown" }, { status: "unknown" }]);
  expect(await session.submit(input)).toEqual({ status: "unknown" });
  expect(api.readFleet).toHaveBeenCalledTimes(1); expect(api.submitReassignment).toHaveBeenCalledExactlyOnceWith({ ...input, expectedObservationId: recoverySnapshot().observationId });
  expect(JSON.stringify(session.status())).not.toContain("private"); expect(api.submitCharging).not.toHaveBeenCalled();
});
it("preserves a Go stale-observation denial after the final read without retrying", async () => {
  const state = recoverySnapshot(); const api = apiFixture(state);
  api.submitReassignment.mockResolvedValue({ ...pendingFixture, state: "", error: "observation is missing or stale; request a new analysis" });
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key");
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryReasoning(state, "R4") } as never);
  const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("accepted"); expect(result.proposal.status).toBe("rejected");
  expect(api.submitReassignment).toHaveBeenCalledExactlyOnceWith({ jobId: "J1", fromRobotId: "R1", toRobotId: "R4", expiresAt: expect.any(String), expectedObservationId: state.observationId });
  expect(api.readFleet).toHaveBeenCalledTimes(2); expect(api.submitCharging).not.toHaveBeenCalled();
});
it("uses only the typed GraphQL submission operation", async () => {
  const transport = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { submitReassignment: pendingFixture } }), { status: 200 }));
  const client = createFleetClient("http://127.0.0.1/graphql", "a".repeat(32), transport);
  expect(await client.submitReassignment({ jobId: "J1", fromRobotId: "R1", toRobotId: "R3", expiresAt: "2026-10-16T12:01:00Z", expectedObservationId: recoverySnapshot().observationId! })).toEqual(pendingFixture);
  const body = JSON.parse(transport.mock.calls[0][1].body); expect(body.query).toContain("submitReassignment"); expect(body.query).not.toMatch(/approve|reject|tick|inject/i);
});
