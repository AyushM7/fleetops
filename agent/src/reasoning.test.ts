import { Agent } from "@mastra/core/agent";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { runLive, runOffline } from "./agent.js";
import { validateReasoning, validateSnapshot } from "./reasoning.js";
import { createFleetTools } from "./tools.js";
import { chargingReasoningFixture, fleetFixture, pendingFixture, reasoningFixture } from "./fixtures.js";
import type { AIReasoning, FleetState } from "./contracts.js";

function incidentSnapshot(): FleetState {
  const state = fleetFixture(14);
  state.tick = 42;
  state.robots[0].jobId = "J1";
  state.activeIncidents = [{ robotId: "R1", type: "low_battery", severity: "high", telemetry: { ...state.robots[0] } }];
  return state;
}
function apiFixture(state = incidentSnapshot()) {
  return { readFleet: vi.fn().mockImplementation(async () => structuredClone(state)), submitReassignment: vi.fn().mockResolvedValue(pendingFixture), submitCharging: vi.fn().mockResolvedValue(pendingFixture) };
}
beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); }));
  vi.stubEnv("OPENAI_API_KEY", "test-only-private-key");
});
afterEach(() => {
  expect(fetch).not.toHaveBeenCalled();
  vi.restoreAllMocks(); vi.unstubAllEnvs(); vi.unstubAllGlobals();
});
describe("snapshot-scoped grounding", () => {
  it("accepts exact Go facts and labels references without claiming stable identity", async () => {
    const state = incidentSnapshot();
    expect(validateReasoning(reasoningFixture(state), state)).toEqual(reasoningFixture(state));
    const offline = await runOffline(apiFixture(state));
    expect(offline.observedFacts.incidentReferences).toEqual([{ scope: "snapshot", tick: 42, robotId: "R1", type: "low_battery" }]);
    expect(offline.observedFacts.referenceNotice).toContain("not stable incident IDs");
    expect(offline.observedFacts.referenceNotice).toContain("separate simulator runs");
    expect(offline.aiAnalysis.status).toBe("disabled");
  });
  it.each([
    ["wrong root tick", (r: AIReasoning) => { r.tick++; }],
    ["wrong reference tick", (r: AIReasoning) => { r.interpretations[0].reference.tick--; }],
    ["invented robot", (r: AIReasoning) => { r.interpretations[0].reference.robotId = "R999"; }],
    ["mismatched incident", (r: AIReasoning) => { r.interpretations[0].reference.type = "traffic_blocked"; }],
    ["missing incident", (r: AIReasoning) => { r.interpretations = []; }],
    ["duplicate incident", (r: AIReasoning) => { r.interpretations.push(structuredClone(r.interpretations[0])); }],
    ["altered telemetry", (r: AIReasoning) => { r.interpretations[0].telemetry.battery++; }],
    ["altered severity", (r: AIReasoning) => { r.interpretations[0].severity = "medium"; }],
  ] as const)("rejects %s", async (_name, change) => {
    const state = incidentSnapshot(); const reasoning = reasoningFixture(state); change(reasoning);
    expect(() => validateReasoning(reasoning, state)).toThrow("grounding");
    vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: reasoning } as never);
    const api = apiFixture(state);
    const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
    expect(result.aiAnalysis).toEqual({ status: "rejected", interpretations: [], proposedAction: null });
    expect(result.recommendedAction).toBeNull();
    expect(api.submitCharging).not.toHaveBeenCalled();
  });
  it("rejects missing references, unknown fields and persistent-ID substitutions", () => {
    const state = incidentSnapshot(); const reasoning = reasoningFixture(state);
    for (const reference of [null, { scope: "snapshot", tick: 42, robotId: "R1" },
      { ...reasoning.interpretations[0].reference, incidentId: "I1" }, { incidentId: "I1" }]) {
      expect(() => validateReasoning({ ...reasoning, interpretations: [{ ...reasoning.interpretations[0], reference }] }, state)).toThrow("grounding");
    }
  });
  it("rejects repeated references even when the interpretation count matches the snapshot", () => {
    const state = incidentSnapshot(); state.robots[0].status = "offline";
    state.activeIncidents[0].telemetry.status = "offline";
    state.activeIncidents.push({ robotId: "R1", type: "robot_unresponsive", severity: "high", telemetry: { ...state.robots[0] } });
    const reasoning = reasoningFixture(state);
    expect(validateReasoning(reasoning, state).interpretations).toHaveLength(2);
    reasoning.interpretations[1] = structuredClone(reasoning.interpretations[0]);
    expect(() => validateReasoning(reasoning, state)).toThrow("grounding");
  });
  it("rejects ambiguous or internally inconsistent snapshots before a model call", async () => {
    for (const change of [
      (s: FleetState) => { s.activeIncidents.push(structuredClone(s.activeIncidents[0])); },
      (s: FleetState) => { s.robots.push({ ...s.robots[0] }); },
      (s: FleetState) => { s.stations.push({ ...s.stations[0] }); },
      (s: FleetState) => { s.activeIncidents[0].telemetry.battery++; },
      (s: FleetState) => { s.activeIncidents[0].robotId = "R999"; },
    ]) {
      const state = incidentSnapshot(); change(state);
      expect(() => validateSnapshot(state)).toThrow("grounding");
      const generate = vi.spyOn(Agent.prototype, "generate");
      await expect(runLive(apiFixture(state), { enabled: true, model: "openai/test" })).rejects.toThrow("grounding");
      expect(generate).not.toHaveBeenCalled();
    }
  });
  it("accepts grounded interpretation without converting it into observed fact", async () => {
    const state = incidentSnapshot(); const reasoning = reasoningFixture(state);
    const generate = vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: reasoning } as never);
    const result = await runLive(apiFixture(state), { enabled: true, model: "openai/test" });
    expect(result.observedFacts.fleet).toEqual(state);
    expect(result.aiAnalysis.interpretations[0].assessment).toBe(reasoning.interpretations[0].assessment);
    expect(result.observations.join(" ")).not.toContain(reasoning.interpretations[0].assessment);
    const [, settings] = generate.mock.calls[0] as unknown as [unknown, Record<string, unknown>];
    expect(settings).toMatchObject({ maxSteps: 1, activeTools: [], toolChoice: "none" });
  });
});
describe("validated actions and fresh-state submission", () => {
  it("keeps charging restricted to the deterministic selection when another robot is also eligible", async () => {
    const state = fleetFixture(50);
    state.robots.push({ ...state.robots[0], id: "R2", x: 1 });
    state.stations.push({ ...state.stations[0], id: "S2", x: 1 });
    const alternative = chargingReasoningFixture({ ...state, robots: [state.robots[1], state.robots[0]], stations: [state.stations[1], state.stations[0]] });
    expect(() => validateReasoning(alternative, state)).toThrow("grounding");
    const api = apiFixture(state);
    const expiresAt = "2026-10-16T12:01:00Z";
    expect(await createFleetTools(api, true).submit({ robotId: "R2", stationId: "S2", expiresAt })).toEqual({ status: "not_requested" });
    expect(api.submitCharging).not.toHaveBeenCalled();
    expect(validateReasoning(chargingReasoningFixture(state), state)).toEqual(chargingReasoningFixture(state));
    expect((await createFleetTools(api, true).submit({ robotId: "R1", stationId: "S1", expiresAt })).status).toBe("pending_approval");
    expect(api.submitCharging).toHaveBeenCalledExactlyOnceWith({ robotId: "R1", stationId: "S1", expiresAt });
    expect(api.submitReassignment).not.toHaveBeenCalled();
  });
  it("validates telemetry evidence, explicit demo intent and supported commands", () => {
    const state = fleetFixture(50); const valid = chargingReasoningFixture(state);
    expect(validateReasoning(valid, state)).toEqual(valid);
    const action = valid.proposedAction!;
    for (const proposedAction of [
      { ...action, action: "MoveRobot" }, { ...action, robotId: "R999" }, { ...action, stationId: "S999" },
      { ...action, purpose: "incident_recovery" }, { ...action, purpose: "demonstration" },
      { ...action, evidence: { ...action.evidence, tick: state.tick + 1 } },
      { ...action, evidence: { ...action.evidence, robot: { ...state.robots[0], battery: 99 } } },
      { ...action, evidence: { ...action.evidence, station: { ...state.stations[0], occupiedBy: "R2" } } },
    ]) expect(() => validateReasoning({ ...valid, proposedAction }, state)).toThrow("grounding");
    const demoState = fleetFixture(); const demo = chargingReasoningFixture(demoState);
    demo.proposedAction!.purpose = "demonstration";
    expect(() => validateReasoning(demo, demoState)).toThrow("grounding");
    expect(validateReasoning(demo, demoState, { robotId: "R1", stationId: "S1" })).toEqual(demo);
  });
  it("never submits an invalid or malformed model action even with opt-in", async () => {
    const state = fleetFixture(50); const reasoning = chargingReasoningFixture(state);
    const invalid = { ...reasoning, proposedAction: { ...reasoning.proposedAction, action: "Tick" } };
    vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: invalid } as never);
    const api = apiFixture(state);
    expect((await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true })).aiAnalysis.status).toBe("rejected");
    expect(api.submitCharging).not.toHaveBeenCalled();
    expect(api.readFleet).toHaveBeenCalledTimes(1);
  });
  it("submits a validated action only after generation, opt-in and a fresh eligibility read", async () => {
    const state = fleetFixture(50); const api = apiFixture(state);
    vi.spyOn(Agent.prototype, "generate").mockImplementation(async () => {
      expect(api.submitCharging).not.toHaveBeenCalled();
      return { object: chargingReasoningFixture(state) } as never;
    });
    const readonly = await runLive(api, { enabled: true, model: "openai/test" });
    expect(readonly.aiAnalysis.status).toBe("accepted");
    expect(api.submitCharging).not.toHaveBeenCalled();
    const submitted = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
    expect(submitted.submissionCheck).toBe("current");
    expect(submitted.proposal.status).toBe("pending_approval");
    expect(api.submitCharging).toHaveBeenCalledTimes(1);
    expect(api.readFleet).toHaveBeenCalledTimes(3);
  });
  it.each([
    ["new tick", (s: FleetState) => { s.tick++; }],
    ["same-tick battery change", (s: FleetState) => { s.robots[0].battery = 29; }],
    ["same-tick station change", (s: FleetState) => { s.stations[0].occupiedBy = "R2"; }],
    ["runtime poisoned", (s: FleetState) => { s.poisoned = true; }],
  ] as const)("aborts a proposal after %s instead of reusing old evidence", async (_name, change) => {
    const state = fleetFixture(50); const current = structuredClone(state); change(current);
    const api = apiFixture(state); api.readFleet.mockResolvedValueOnce(state).mockResolvedValueOnce(current);
    vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: chargingReasoningFixture(state) } as never);
    const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
    expect(result.submissionCheck).toBe("snapshot_changed");
    expect(result.recommendedAction).toBeNull();
    expect(result.proposal.status).toBe("not_requested");
    expect(api.submitCharging).not.toHaveBeenCalled();
  });
  it("does not submit if the fresh read fails or if the provider fails", async () => {
    const state = fleetFixture(50); const api = apiFixture(state);
    api.readFleet.mockResolvedValueOnce(state).mockRejectedValueOnce(new Error("private-transport-error"));
    const generate = vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: chargingReasoningFixture(state) } as never);
    const freshFailure = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
    expect(freshFailure.submissionCheck).toBe("unavailable");
    generate.mockRejectedValueOnce(new Error("private-provider-error"));
    const failure = await runLive(apiFixture(state), { enabled: true, model: "openai/test", allowSubmit: true });
    expect(failure.aiAnalysis.status).toBe("failed");
    expect(JSON.stringify([freshFailure, failure])).not.toContain("private-");
    expect(api.submitCharging).not.toHaveBeenCalled();
  });
});
