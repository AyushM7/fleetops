import { randomBytes } from "node:crypto";
import { Agent } from "@mastra/core/agent";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createFleetClient } from "./graphql.js";
import { runLive, runOffline } from "./agent.js";
import { chargingReasoningFixture, offlineModelFixture, recoveryDecisionFixture } from "./fixtures.js";
import { runRecoveryDecision } from "./decision-agent.js";

// This suite deliberately fails without an isolated, station-ready real backend.
const endpoint = process.env.FLEETOPS_TEST_GRAPHQL_URL;
const token = process.env.FLEETOPS_TEST_PROPOSER_TOKEN;
const viewer = process.env.FLEETOPS_TEST_VIEWER_TOKEN;
if (!endpoint || !token || !viewer) throw new Error("Run scripts/verify-integration.ps1; isolated backend configuration is required.");
const api = createFleetClient(endpoint, token);
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllEnvs(); });
describe("real Go GraphQL + PostgreSQL integration (no model)", () => {
  it("finalizes v2 abstention against a real observation using a credential-free local model adapter", async () => {
    const state = await api.readFleet(), call = vi.fn();
    const output = recoveryDecisionFixture(state, "compare_tradeoffs", null);
    const result = await runRecoveryDecision(api, { enabled: true, model: offlineModelFixture(output, call), source: "mocked", objective: "compare_tradeoffs", allowSubmit: true });
    expect(result.aiAnalysis).toEqual({ status: "accepted", decision: output });
    expect(result.proposal.status).toBe("not_requested"); expect(call).toHaveBeenCalledTimes(2);
    expect(await api.readFleet()).toEqual(state);
  });
  it("reads real state using authenticated proposer access", async () => {
    const state = await api.readFleet();
    expect(state.poisoned).toBe(false);
    expect(state.activeIncidents).toEqual([]);
    expect(state.robots.some(r => r.id === "R1")).toBe(true);
    const robot = state.robots.find(r => r.id === "R1")!;
    const station = state.stations.find(s => s.id === "S1")!;
    expect([robot.x, robot.y]).toEqual([station.x, station.y]);
    expect(robot.battery).toBe(100);
    const observation = await runOffline(api);
    expect(observation.proposal.status).toBe("disabled");
    expect(observation.incidents).toEqual([]);
    expect(observation.operatorAttention).toEqual([]);
    expect(await api.readFleet()).toEqual(state);
  });
  it("submits a durable demonstration pending human approval without execution", async () => {
    const result = await runOffline(api, { allowSubmit: true, demo: { robotId: "R1", stationId: "S1" } });
    expect(result.proposal.status).toBe("pending_approval");
    expect(result.proposal.proposalId).toBeTruthy();
    expect(result.potentialIssues).toEqual([]);
    expect(result.explanation).toContain("demonstration");
    expect((await api.readFleet()).robots.find(r => r.id === "R1")!.status).toBe("idle");
  });
  it("handles a real invalid proposal as a committed denial", async () => {
    const result = await api.submitCharging({ robotId: "missing-robot", stationId: "S1", expiresAt: new Date(Date.now() + 60_000).toISOString(), expectedObservationId: (await api.readFleet()).observationId! });
    expect(result.error).toBeTruthy();
    expect(result.auditCommitted).toBe(true);
    expect(result.executionAttempted).toBe(false);
  });
  it("grounds a model-double recommendation in real fleet telemetry before a durable demo submission", async () => {
    const state = await api.readFleet();
    const reasoning = chargingReasoningFixture(state);
    reasoning.proposedAction!.purpose = "demonstration";
    const generate = vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: reasoning } as never);
    vi.stubEnv("OPENAI_API_KEY", "test-only-key");
    const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true, demo: { robotId: "R1", stationId: "S1" } });
    expect(result.aiAnalysis.status).toBe("accepted");
    expect(result.observedFacts.fleet).toEqual(state);
    expect(result.submissionCheck).toBe("current");
    expect(result.proposal.status).toBe("pending_approval");
    expect(generate).toHaveBeenCalledTimes(1);
    expect((await api.readFleet()).robots.find(r => r.id === "R1")!.status).toBe("idle");
  });
  it("rejects invalid authentication and viewer submission", async () => {
    await expect(createFleetClient(endpoint!, randomBytes(32).toString("hex")).readFleet()).rejects.toThrow("authentication");
    const result = await createFleetClient(endpoint!, viewer!).submitCharging({ robotId: "R1", stationId: "S1", expiresAt: new Date(Date.now() + 60_000).toISOString(), expectedObservationId: (await api.readFleet()).observationId! });
    expect(result.error).toBe("unauthorized");
    expect(result.executionAttempted).toBe(false);
    expect(result.auditCommitted).toBe(true);
  });
});
