import { randomBytes } from "node:crypto";
import { describe, expect, it } from "vitest";
import { createFleetClient } from "./graphql.js";
import { runOffline } from "./agent.js";

// This suite deliberately fails without an isolated, station-ready real backend.
const endpoint = process.env.FLEETOPS_TEST_GRAPHQL_URL;
const token = process.env.FLEETOPS_TEST_PROPOSER_TOKEN;
const viewer = process.env.FLEETOPS_TEST_VIEWER_TOKEN;
if (!endpoint || !token || !viewer) throw new Error("Run scripts/verify-integration.ps1; isolated backend configuration is required.");
const api = createFleetClient(endpoint, token);
describe("real Go GraphQL + PostgreSQL integration (no model)", () => {
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
    const result = await api.submitCharging({ robotId: "missing-robot", stationId: "S1", expiresAt: new Date(Date.now() + 60_000).toISOString() });
    expect(result.error).toBeTruthy();
    expect(result.auditCommitted).toBe(true);
    expect(result.executionAttempted).toBe(false);
  });
  it("rejects invalid authentication and viewer submission", async () => {
    await expect(createFleetClient(endpoint!, randomBytes(32).toString("hex")).readFleet()).rejects.toThrow("authentication");
    const result = await createFleetClient(endpoint!, viewer!).submitCharging({ robotId: "R1", stationId: "S1", expiresAt: new Date(Date.now() + 60_000).toISOString() });
    expect(result.error).toBe("unauthorized");
    expect(result.executionAttempted).toBe(false);
    expect(result.auditCommitted).toBe(true);
  });
});
