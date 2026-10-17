import { Agent } from "@mastra/core/agent";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createFleetAgent, runLive, runOffline } from "./agent.js";
import { createFleetTools } from "./tools.js";
import { observeFleet } from "./observe.js";
import { readConfig } from "./config.js";
import { fleetFixture, pendingFixture, reasoningFixture } from "./fixtures.js";

const input = { robotId: "R1", stationId: "S1", expiresAt: "2026-10-16T12:01:00Z" };
function apiFixture(battery = 100) {
  return { readFleet: vi.fn().mockResolvedValue(fleetFixture(battery)), submitReassignment: vi.fn().mockResolvedValue(pendingFixture), submitCharging: vi.fn().mockResolvedValue(pendingFixture) };
}
beforeEach(() => { vi.stubGlobal("fetch", vi.fn(() => { throw new Error("Unexpected network"); })); });
afterEach(() => {
  expect(fetch).not.toHaveBeenCalled();
  vi.restoreAllMocks(); vi.unstubAllEnvs(); vi.unstubAllGlobals();
});
describe("agent boundaries", () => {
  it("defaults to deterministic observations without model or proposal requests", async () => {
    vi.stubGlobal("fetch", vi.fn(() => { throw new Error("Unexpected network"); }));
    const generate = vi.spyOn(Agent.prototype, "generate").mockRejectedValue(new Error("Unexpected model call"));
    const api = apiFixture();
    expect(await runOffline(api)).toEqual(await runOffline(api));
    const result = await runOffline(api);
    expect(result.recommendedAction).toBeNull();
    expect(result.potentialIssues).toEqual([]);
    expect(result.proposal.status).toBe("disabled");
    expect(api.submitCharging).not.toHaveBeenCalled();
    expect(generate).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });
  it("exposes only a read tool to Mastra and cannot enable submission through input", async () => {
    const api = apiFixture();
    const { agent, session } = createFleetAgent(api, "openai/test");
    expect(Object.keys(await agent.listTools())).toEqual(["readFleetState"]);
    expect(await session.tools.readFleetState.execute!({}, {} as never)).toEqual(fleetFixture());
    expect(await session.tools.submitSetCharging.execute!({ ...input, allowSubmit: true } as typeof input, {} as never)).toEqual({ status: "disabled" });
    expect(api.submitCharging).not.toHaveBeenCalled();
  });
  it("submits only with explicit opt-in and labels a fully charged demo", async () => {
    const api = apiFixture();
    const result = await runOffline(api, { allowSubmit: true, demo: { robotId: "R1", stationId: "S1" }, now: () => new Date("2026-10-16T12:00:00Z") });
    expect(result.explanation).toContain("demonstration");
    expect(result.potentialIssues).toEqual([]);
    expect(result.proposal).toEqual({ status: "pending_approval", proposalId: "p1" });
    expect(api.submitCharging).toHaveBeenCalledWith({ ...input, expiresAt: "2026-10-16T12:01:00.000Z" });
  });
  it.each([
    [{ ...pendingFixture, error: "denied", state: "", auditCommitted: true }, "rejected"],
    [{ ...pendingFixture, auditCommitted: false }, "unknown"],
    [{ ...pendingFixture, executionAttempted: true }, "unknown"],
    [{ ...pendingFixture, proposalId: "" }, "unknown"],
  ])("does not mistake HTTP success for durable acceptance", async (response, status) => {
    const api = apiFixture(50); api.submitCharging.mockResolvedValue(response);
    const session = createFleetTools(api, true);
    expect((await session.submit(input)).status).toBe(status);
  });
  it("never retries uncertain or concurrent submission", async () => {
    const api = apiFixture(50); api.submitCharging.mockRejectedValue(new Error("private failure"));
    const session = createFleetTools(api, true);
    expect(await Promise.all([session.submit(input), session.submit(input)])).toEqual([{ status: "unknown" }, { status: "unknown" }]);
    expect(api.submitCharging).toHaveBeenCalledTimes(1);
  });
  it("does not classify incidents in TypeScript and reports poisoned runtime separately", () => {
    const state = fleetFixture(29); state.robots[0].blocked = true;
    expect(observeFleet(state).potentialIssues).toEqual([]);
    expect(observeFleet(state).recommendedAction).toBeNull();
    state.poisoned = true;
    expect(observeFleet(state, { robotId: "R1", stationId: "S1" }).recommendedAction).toBeNull();
  });
  it("recommends charging in place only when snapshot facts support it", async () => {
    const api = apiFixture(50);
    expect((await runOffline(api)).recommendedAction).toEqual({ action: "SetCharging", robotId: "R1", stationId: "S1" });
    expect(api.submitCharging).not.toHaveBeenCalled();
    for (const change of [
      (s: ReturnType<typeof fleetFixture>) => { s.robots[0].x = 1; },
      (s: ReturnType<typeof fleetFixture>) => { s.robots[0].jobId = "job"; },
      (s: ReturnType<typeof fleetFixture>) => { s.stations[0].occupiedBy = "R2"; },
      (s: ReturnType<typeof fleetFixture>) => { s.stations[0].reservedBy = "R2"; },
    ]) {
      const state = fleetFixture(50); change(state);
      expect(observeFleet(state).recommendedAction).toBeNull();
    }
  });
  it("requires live opt-in and a real configured key before model execution", async () => {
    const generate = vi.spyOn(Agent.prototype, "generate").mockRejectedValue(new Error("Unexpected model call"));
    const api = apiFixture();
    vi.stubEnv("OPENAI_API_KEY", "");
    await expect(runLive(api, { enabled: true, model: "openai/test" })).rejects.toThrow("opt-in");
    vi.stubEnv("OPENAI_API_KEY", "test-only-key");
    await expect(runLive(api, { enabled: false, model: "openai/test" })).rejects.toThrow("opt-in");
    expect(generate).not.toHaveBeenCalled(); expect(api.readFleet).not.toHaveBeenCalled();
  });
  it("also gates model resolution when the Mastra factory is used directly", async () => {
    vi.stubEnv("OPENAI_API_KEY", "test-only-key");
    await expect(createFleetAgent(apiFixture(), "openai/test").agent.getModel()).rejects.toThrow("opt-in");
    vi.stubEnv("OPENAI_API_KEY", "");
    await expect(createFleetAgent(apiFixture(), "openai/test", false, true).agent.getModel()).rejects.toThrow("configured provider");
  });
  it("uses a deterministic model double, excludes private env and owns proposal status", async () => {
    vi.stubEnv("OPENAI_API_KEY", "test-only-private-key");
    vi.stubEnv("FLEETOPS_PROPOSER_TOKEN", "test-only-private-token");
    const analysis = observeFleet(fleetFixture());
    const generate = vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: reasoningFixture(fleetFixture()) } as never);
    const result = await runLive(apiFixture(), { enabled: true, model: "openai/test" });
    expect(result.observations).toEqual(analysis.observations);
    expect(result.proposal).toEqual({ status: "disabled" });
    expect(result.aiAnalysis.status).toBe("accepted");
    expect(JSON.stringify(generate.mock.calls)).not.toContain("test-only-private");
  });
  it("cannot submit during reasoning and preserves facts when the model fails", async () => {
    vi.stubEnv("OPENAI_API_KEY", "test-only-key");
    vi.spyOn(Agent.prototype, "generate").mockImplementation(async function (this: ReturnType<typeof createFleetAgent>["agent"]) {
      const tools = await this.listTools();
      expect(Object.keys(tools)).toEqual(["readFleetState"]);
      throw new Error("private-provider-failure");
    });
    const api = apiFixture(50);
    const result = await runLive(api, { enabled: true, model: "openai/test", allowSubmit: true });
    expect(result.proposal.status).toBe("not_requested");
    expect(result.aiAnalysis.status).toBe("failed");
    expect(JSON.stringify(result)).not.toContain("private-provider");
    expect(api.submitCharging).not.toHaveBeenCalled();
  });
  it("parses strict user flags and defaults both opt-ins off", () => {
    expect(readConfig({ FLEETOPS_PROPOSER_TOKEN: "private" }, [])).toMatchObject({ live: false, allowSubmit: false });
    expect(() => readConfig({}, [])).toThrow("PROPOSER_TOKEN");
    expect(() => readConfig({ FLEETOPS_PROPOSER_TOKEN: "private" }, ["--demo"])).toThrow("explicit");
    expect(() => readConfig({ FLEETOPS_PROPOSER_TOKEN: "private" }, ["--live"])).toThrow("provider/model");
    expect(() => readConfig({ FLEETOPS_PROPOSER_TOKEN: "private" }, ["--approve"])).toThrow("Unknown");
  });
});
