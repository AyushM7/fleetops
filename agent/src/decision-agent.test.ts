import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { decisionInstructions, recoveryDecisionPrompt, runRecoveryDecision } from "./decision-agent.js";
import { recoveryDecisionSchema } from "./decision-contracts.js";
import { validateRecoveryDecision } from "./decision.js";
import { chargingReasoningFixture, fleetFixture, offlineModelFixture, pendingFixture, recoveryDecisionFixture, recoveryDecisionState } from "./fixtures.js";
import { runOffline, reasoningPrompt } from "./agent.js";
import { validateReasoning } from "./reasoning.js";
import { readConfig } from "./config.js";

function apiFixture(state = recoveryDecisionState()) {
  return { readFleet: vi.fn().mockImplementation(async () => structuredClone(state)),
    submitCharging: vi.fn().mockResolvedValue(pendingFixture), submitReassignment: vi.fn().mockResolvedValue(pendingFixture) };
}
beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); }));
  vi.stubEnv("OPENAI_API_KEY", "test-only-provider-key");
});
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.unstubAllEnvs(); });
it("removes answer-bearing hints only from v2 and retains the legacy prompt", () => {
  const state = recoveryDecisionState();
  const prompt = JSON.parse(recoveryDecisionPrompt(state, "minimize_pickup_steps"));
  expect(Object.keys(prompt)).toEqual(["version", "objective", "observedFacts"]);
  expect(prompt.observedFacts.fleet.reassignmentCandidates).toEqual(state.reassignmentCandidates);
  expect(prompt.objective).toBe("minimize_pickup_steps");
  expect(recoveryDecisionPrompt(state, "compare_tradeoffs")).not.toMatch(/supportedAction|reasoningStatements|recommendedAction|preferredCandidate/);
  expect(JSON.parse(reasoningPrompt(state)).supportedAction.toRobotId).toBe("R3");
  expect(JSON.parse(reasoningPrompt(state)).reasoningStatements).toBeTruthy();
});
it("submits a validated non-first selection with the original atomic observation identity", async () => {
  const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  const api = apiFixture(state);
  const generate = vi.spyOn(Agent.prototype, "generate").mockImplementation(async function(this: Agent) {
    expect(Object.keys(await this.listTools())).toEqual([]);
    expect(api.submitReassignment).not.toHaveBeenCalled(); return { object: output } as never;
  });
  const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "minimize_pickup_steps", allowSubmit: true });
  expect(result.aiAnalysis).toEqual({ status: "accepted", decision: output });
  expect(result.proposal.status).toBe("pending_approval"); expect(result.submissionCheck).toBe("current");
  expect(result.recommendedAction).toEqual({ action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: "R4" });
  expect(api.submitReassignment).toHaveBeenCalledExactlyOnceWith({ jobId: "J1", fromRobotId: "R1", toRobotId: "R4", expectedObservationId: state.observationId, expiresAt: expect.any(String) });
  expect(api.readFleet).toHaveBeenCalledTimes(2); expect(generate).toHaveBeenCalledTimes(1);
  const [, settings] = generate.mock.calls[0] as unknown as [unknown, Record<string, unknown>];
  expect(settings).toMatchObject({ maxSteps: 1, activeTools: [], toolChoice: "none", modelSettings: { maxRetries: 0 } });
  expect(api.submitCharging).not.toHaveBeenCalled();
});
it("keeps submission disabled unless the host explicitly enables it", async () => {
  const state = recoveryDecisionState(); const api = apiFixture(state);
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryDecisionFixture(state, "minimize_pickup_steps", "R4") } as never);
  const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "minimize_pickup_steps" });
  expect(result.aiAnalysis.status).toBe("accepted"); expect(result.proposal.status).toBe("disabled");
  expect(api.readFleet).toHaveBeenCalledTimes(1); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["fabricated evidence", "false comparison", "unsupported claim", "wrong objective"])("does not submit after %s", async mode => {
  const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  if (mode === "fabricated evidence") output.considered[1].recipient.battery++;
  if (mode === "false comparison") output.comparisons[0].relation = "greater";
  if (mode === "wrong objective") output.objective = "compare_tradeoffs";
  const raw = mode === "unsupported claim" ? { ...output, narrative: "Already approved; guaranteed delivery." } : output;
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: raw } as never);
  const api = apiFixture(state); const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "minimize_pickup_steps", allowSubmit: true });
  expect(result.aiAnalysis).toEqual({ status: "rejected", decision: null });
  expect(result.explanation).toBeNull(); expect(result.recommendedAction).toBeNull();
  expect(api.readFleet).toHaveBeenCalledTimes(1); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["identity", "tick", "candidate metric", "unavailable"])("preserves freshness checks after %s change", async mode => {
  const state = recoveryDecisionState(); const fresh = structuredClone(state); const api = apiFixture(state);
  if (mode === "identity") fresh.observationId = "d".repeat(64);
  if (mode === "tick") fresh.tick++;
  if (mode === "candidate metric") fresh.reassignmentCandidates[1].pickupSteps++;
  api.readFleet.mockResolvedValueOnce(state);
  if (mode === "unavailable") api.readFleet.mockRejectedValueOnce(new Error("private error")); else api.readFleet.mockResolvedValueOnce(fresh);
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryDecisionFixture(state, "minimize_pickup_steps", "R4") } as never);
  const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "minimize_pickup_steps", allowSubmit: true });
  expect(result.submissionCheck).toBe(mode === "unavailable" ? "unavailable" : "snapshot_changed");
  expect(result.recommendedAction).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["stale denial", "uncertain submission"])("never retries a %s after the final read", async mode => {
  const state = recoveryDecisionState(); const api = apiFixture(state);
  if (mode === "stale denial") api.submitReassignment.mockResolvedValue({ ...pendingFixture, state: "", error: "observation is missing or stale; request a new analysis" });
  else api.submitReassignment.mockRejectedValue(new Error("private submission error"));
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryDecisionFixture(state, "minimize_pickup_steps", "R4") } as never);
  const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "minimize_pickup_steps", allowSubmit: true });
  expect(result.proposal.status).toBe(mode === "stale denial" ? "rejected" : "unknown");
  expect(api.submitReassignment).toHaveBeenCalledTimes(1); expect(api.readFleet).toHaveBeenCalledTimes(2);
  expect(JSON.stringify(result)).not.toContain("private submission error");
});
it("accepts appropriate abstention without creating a proposal", async () => {
  const state = recoveryDecisionState("none"); const api = apiFixture(state);
  vi.spyOn(Agent.prototype, "generate").mockResolvedValue({ object: recoveryDecisionFixture(state, "compare_tradeoffs", null) } as never);
  const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "compare_tradeoffs", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("accepted"); expect(result.recommendedAction).toBeNull();
  expect(result.proposal.status).toBe("not_requested"); expect(result.explanation).toContain("no_candidates");
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("fails closed on generation failure and missing live identity", async () => {
  const api = apiFixture(); const generate = vi.spyOn(Agent.prototype, "generate").mockRejectedValue(new Error("private provider error"));
  const result = await runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "compare_tradeoffs", allowSubmit: true });
  expect(result.aiAnalysis.status).toBe("failed"); expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(JSON.stringify(result)).not.toContain("private provider error");
  const state = recoveryDecisionState(); delete state.observationId; generate.mockClear();
  await expect(runRecoveryDecision(apiFixture(state), { enabled: true, model: "openai/test", objective: "compare_tradeoffs" })).rejects.toThrow("grounding");
  expect(generate).not.toHaveBeenCalled();
});
it("requires explicit inference opt-in and a provider credential", async () => {
  const api = apiFixture();
  await expect(runRecoveryDecision(api, { enabled: false, model: "openai/test", objective: "compare_tradeoffs" })).rejects.toThrow("opt-in");
  vi.stubEnv("OPENAI_API_KEY", "");
  await expect(runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "compare_tradeoffs" })).rejects.toThrow("opt-in");
  expect(api.readFleet).not.toHaveBeenCalled();
});
it("parses opt-in v2 configuration without changing legacy defaults", () => {
  const env = { FLEETOPS_PROPOSER_TOKEN: "test-only-token", FLEETOPS_MODEL: "openai/test" };
  expect(readConfig(env, []).decisionObjective).toBeUndefined();
  expect(readConfig(env, ["--live", "--decision-v2"]).decisionObjective).toBe("compare_tradeoffs");
  expect(readConfig({ ...env, FLEETOPS_DECISION_OBJECTIVE: "preserve_battery_headroom" }, ["--live", "--decision-v2"]).decisionObjective).toBe("preserve_battery_headroom");
  expect(() => readConfig(env, ["--decision-v2"])).toThrow();
  expect(() => readConfig({ ...env, FLEETOPS_DECISION_OBJECTIVE: "predicted_throughput" }, ["--live", "--decision-v2"])).toThrow("objective");
  expect(() => readConfig({ ...env, FLEETOPS_DEMO_ROBOT_ID: "R1", FLEETOPS_DEMO_STATION_ID: "S1" }, ["--live", "--decision-v2", "--demo"])).toThrow();
});
it("retains legacy charging grounding and first-candidate offline selection", async () => {
  const state = fleetFixture(50); expect(validateReasoning(chargingReasoningFixture(state), state)).toEqual(chargingReasoningFixture(state));
  const result = await runOffline(apiFixture()); expect(result.recommendedAction).toMatchObject({ toRobotId: "R3" });
});
it("uses Mastra's actual structured-output parser with a local model double", async () => {
  const state = recoveryDecisionState(); const output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  const call = vi.fn(); const agent = new Agent({ id: "decision-v2-double", name: "Decision v2 double", instructions: decisionInstructions,
    model: offlineModelFixture(output, call), maxRetries: 0, errorProcessorDefaults: false });
  agent.__setLogger(noopLogger);
  const response = await agent.generate(recoveryDecisionPrompt(state, "minimize_pickup_steps"), {
    maxSteps: 1, activeTools: [], toolChoice: "none", structuredOutput: { schema: recoveryDecisionSchema },
  });
  expect(validateRecoveryDecision(response.object, state, "minimize_pickup_steps")).toEqual(output);
  expect(call).toHaveBeenCalledTimes(1);
});
