import { Agent } from "@mastra/core/agent";
import { afterEach, describe, expect, it, vi } from "vitest";
import { runLive, runOffline } from "./agent.js";
import { createFleetTools } from "./tools.js";
import { observeFleet } from "./observe.js";
import { createFleetClient } from "./graphql.js";
import { fleetFixture, pendingFixture } from "./fixtures.js";
import type { ActiveIncident } from "./contracts.js";

// Supplied Go classifications are fixtures, not a TypeScript detector.
function incidentFleet(type: ActiveIncident["type"], severity: ActiveIncident["severity"]) {
  const state = fleetFixture(100);
  const r = state.robots[0];
  if (type === "low_battery") { r.battery = 14; r.jobId = "J1"; }
  if (type === "robot_unresponsive") r.status = "offline";
  if (type === "traffic_blocked") { r.blocked = true; r.waitTicks = 41; }
  state.activeIncidents = [{ robotId: r.id, type, severity, telemetry: { ...r } }];
  return state;
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.unstubAllEnvs(); });
describe("consumption of Go-classified incidents", () => {
  it.each(["low_battery", "robot_unresponsive", "traffic_blocked"] as const)("requires operator attention for %s without a proposal even after opt-in", async type => {
    const state = incidentFleet(type, "high");
    const api = { readFleet: vi.fn().mockResolvedValue(state), submitCharging: vi.fn().mockResolvedValue(pendingFixture) };
    const first = await runOffline(api, { allowSubmit: true });
    expect(await runOffline(api, { allowSubmit: true })).toEqual(first);
    expect(first.incidents).toEqual(state.activeIncidents);
    expect(first.incidents[0].telemetry).toEqual(state.robots[0]);
    expect(first.recommendedAction).toBeNull();
    expect(first.operatorAttention).toEqual([{ robotId: "R1", incidentType: type, reason: expect.stringContaining("No supported") }]);
    expect(first.proposal.status).toBe("not_requested");
    expect(api.submitCharging).not.toHaveBeenCalled();
  });
  it("does not calculate incident severity or create incidents from raw telemetry", () => {
    const state = incidentFleet("traffic_blocked", "medium");
    expect(observeFleet(state).incidents[0].severity).toBe("medium");
    state.activeIncidents = [];
    expect(observeFleet(state).incidents).toEqual([]);
    expect(observeFleet(state).potentialIssues).toEqual([]);
  });
  it("does not offer unrelated routine charging as incident recovery", () => {
    const state = incidentFleet("robot_unresponsive", "high");
    state.robots.push({ ...fleetFixture(50).robots[0], id: "R2" });
    expect(observeFleet(state).recommendedAction).toBeNull();
  });
  it("blocks a model tool's unsupported target and keeps submission disabled by default", async () => {
    const api = { readFleet: vi.fn().mockResolvedValue(incidentFleet("low_battery", "high")), submitCharging: vi.fn() };
    const session = createFleetTools(api, true);
    expect((await session.submit({ robotId: "R1", stationId: "S1", expiresAt: "2026-10-16T12:01:00Z" })).status).toBe("not_requested");
    expect(api.submitCharging).not.toHaveBeenCalled();
    api.readFleet.mockRejectedValue(new Error("must not read when disabled"));
    expect((await createFleetTools(api).submit({ robotId: "R1", stationId: "S1", expiresAt: "2026-10-16T12:01:00Z" })).status).toBe("disabled");
  });
  it("keeps classifications and supported recommendations under host control with a model double", async () => {
    vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); }));
    vi.stubEnv("OPENAI_API_KEY", "test-only-key");
    const state = incidentFleet("low_battery", "high");
    const api = { readFleet: vi.fn().mockResolvedValue(state), submitCharging: vi.fn() };
    const generate = vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: {
      observations: ["Snapshot reviewed."], potentialIssues: ["invented"],
      recommendedAction: { action: "SetCharging", robotId: "R1", stationId: "S1" }, explanation: "Operator attention required.",
      incidents: [],
    } } as never);
    const result = await runLive(api, { enabled: true, model: "openai/test" });
    expect(result.incidents).toEqual(state.activeIncidents);
    expect(result.recommendedAction).toBeNull();
    expect(result.potentialIssues).not.toContain("invented");
    expect(JSON.stringify(generate.mock.calls[0][0])).toContain("low_battery");
    expect(api.submitCharging).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });
  it.each([
    { activeIncidents: undefined },
    { activeIncidents: [{ robotId: "R1", type: "invented", severity: "high", telemetry: fleetFixture().robots[0] }] },
    { activeIncidents: [{ robotId: "R1", type: "low_battery", severity: "critical", telemetry: {} }] },
  ])("fails closed on unavailable or malformed incident data", async patch => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(Response.json({ data: { fleetState: { ...fleetFixture(), ...patch } } }));
    const api = createFleetClient("http://127.0.0.1:8080/graphql", "a".repeat(64), transport);
    await expect(runOffline(api, { allowSubmit: true })).rejects.toThrow("malformed");
    expect(transport).toHaveBeenCalledTimes(1);
  });
});
