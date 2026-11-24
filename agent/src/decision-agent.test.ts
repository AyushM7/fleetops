import { Agent } from "@mastra/core/agent";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { writeFileSync } from "node:fs";
import { decisionInstructions, recoveryDecisionPrompt, runRecoveryDecision, runRecoveryDecisionV3 } from "./decision-agent.js";
import { reviewOperationsRecovery } from "./operations-manager.js";
import { groundRecoveryIntent, type RecoveryIntent } from "./recovery-decision-v3.js";
import { validateRecoveryDecision } from "./decision.js";
import * as decisions from "./decision.js";
import { chargingReasoningFixture, fleetFixture, offlineModelFixture, pendingFixture, recoveryDecisionFixture, recoveryDecisionState } from "./fixtures.js";
import { runOffline, reasoningPrompt } from "./agent.js";
import { snapshotReference, validateReasoning } from "./reasoning.js";
import { readConfig } from "./config.js";
import type { FleetState } from "./contracts.js";
import { InvestigationBudget } from "./investigation-budget.js";
import { createInvestigationTools } from "./investigation-tools.js";
import * as investigationTools from "./investigation-tools.js";

type Model = Extract<Awaited<ReturnType<Agent["getModel"]>>, { specificationVersion: "v2" }>;
type Request = Parameters<Model["doGenerate"]>[0];
type Call = { name: string; args: unknown };
type Step = Call[] | string | Error;
function scripted(steps: Step[], onRequest: (request: Request, index: number) => void = () => {}) {
  const requests: Request[] = [];
  const model = { ...offlineModelFixture(null), async doGenerate(request: Request) {
    const index = requests.length; requests.push(request); onRequest(request, index);
    const step = steps[index]; if (step instanceof Error) throw step;
    if (step === undefined) throw new Error("Unexpected model request");
    const content = typeof step === "string" ? [{ type: "text" as const, text: step }] : step.map((call, id) => ({
      type: "tool-call" as const, toolCallId: `${index}-${id}`, toolName: call.name, input: JSON.stringify(call.args),
    }));
    return { content, finishReason: typeof step === "string" ? "stop" as const : "tool-calls" as const,
      usage: { inputTokens: 100, outputTokens: 100, totalTokens: 200 }, warnings: [] };
  } };
  return { model, requests };
}
function stepsFor(state: FleetState, output: unknown): Step[] {
  const discovery = state.activeIncidents.flatMap(i => {
    const reference = snapshotReference(state, i);
    return [{ name: "readIncident", args: { reference } }, { name: "listRecoveryCandidates", args: { reference } }];
  });
  const ids = [...new Set(state.reassignmentCandidates.flatMap(c => [c.fromRobotId, c.toRobotId]))];
  return [discovery.length ? discovery : "No incidents.", ...(ids.length ? [[{ name: "readRobots", args: { ids } },
    { name: "readJobs", args: { ids: [...new Set(state.reassignmentCandidates.map(c => c.jobId))] } }]] : []), JSON.stringify(output)];
}
function apiFixture(state = recoveryDecisionState()) {
  const api = { readFleet: vi.fn().mockImplementation(async () => structuredClone(state)),
    submitCharging: vi.fn().mockResolvedValue(pendingFixture), submitReassignment: vi.fn().mockResolvedValue(pendingFixture) };
  // Emulate Go's atomic completion endpoint while retaining the existing admission-response assertions.
  return { ...api, startAgentRun: vi.fn(async (observationId: string, _metadata?: import("./agent-run.js").AgentRunMetadata) => ({ id: "Arun", runtimeId: "runtime", observationId })),
    finishAgentRun: vi.fn(async (id: string, input: import("./agent-run.js").AgentRunFinish) => ({ runId: id, recorded: true as const, denial: "",
      proposalResult: input.submit ? await api.submitReassignment({ ...input.completion.decision!.selected!, expectedObservationId: input.observationId, expiresAt: input.submit.expiresAt }) : null })) };
}
function options(model: ReturnType<typeof scripted>["model"]) {
  return { enabled: true, model, source: "mocked" as const, objective: "minimize_pickup_steps" as const, allowSubmit: true };
}
function complete(state = recoveryDecisionState(), output: unknown = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4")) {
  return scripted(stepsFor(state, output));
}
function intentFor(state: FleetState, toRobotId: string | null, objective: RecoveryIntent["objective"] = "minimize_pickup_steps"): RecoveryIntent {
  const fixture = recoveryDecisionFixture(state, objective, toRobotId);
  return { version: "recovery-decision-v3", objective, decision: fixture.decision,
    relevantIncidents: fixture.incidents.map(({ reference }) => ({ robotId: reference.robotId, type: reference.type })),
    comparisons: fixture.comparisons.map(({ left, right, metric }) => ({ left, right, metric })) };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
beforeEach(() => { vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); })); });
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.useRealTimers(); });

it("derives V3 facts, comparisons and invariant limitations while keeping Operations review in shadow", async () => {
  const state = recoveryDecisionState(), intent = intentFor(state, "R4"), api = apiFixture(state);
  const { model, requests } = complete(state, intent);
  const result = await reviewOperationsRecovery(api, { observationId: state.observationId! }, options(model));
  expect(result).toMatchObject({ version: "recovery-decision-v3", intent,
    aiAnalysis: { status: "accepted", decision: recoveryDecisionFixture(state, intent.objective, "R4") },
    proposal: { status: "disabled" }, provenance: { status: "recorded" } });
  expect(requests.map(request => request.toolChoice?.type)).toEqual(["auto", "auto", "none"]);
  expect(result.investigation.toolCalls).toBe(6);
  expect(api.startAgentRun.mock.calls[0][1]?.contract).toBe("recovery-decision-v3");
  expect(api.finishAgentRun.mock.calls[0][1].submit).toBeNull();
  const stale = structuredClone(result.investigation);
  (stale.events.find(event => event.kind === "tool")!.output as { tick: number }).tick++;
  expect(() => groundRecoveryIntent(intent, state, [intent.objective], stale)).toThrow("grounding");
});

it.each(["motor_failure", "sensor_failure", "critical_battery"] as const)("preserves %s evidence in V3 and leaves the V2 contract unchanged", async type => {
  const state = fleetFixture();
  const telemetry = { ...state.robots[0], status: "faulted", battery: type === "critical_battery" ? 0 : state.robots[0].battery };
  state.robots[0] = telemetry;
  state.activeIncidents = [{ robotId: telemetry.id, type, severity: "high", telemetry }];
  state.reassignmentCandidates = [];
  state.observationId = "b".repeat(64);
  const api = apiFixture(state), intent = intentFor(state, null);
  const { model, requests } = complete(state, intent);
  const result = await runRecoveryDecisionV3(api, options(model));
  expect(result.aiAnalysis).toMatchObject({ status: "accepted", decision: { decision: { kind: "abstain", reason: "no_supported_incident" } } });
  expect(result.investigation.events.find(event => event.name === "readIncident")?.output).toMatchObject({ incident: { type } });
  expect(result.investigation.toolCalls).toBe(2);
  expect(api.finishAgentRun.mock.calls[0][1].submit).toBeNull();
  await expect(runRecoveryDecision(api, options(model))).rejects.toThrow("grounding");
  expect(api.startAgentRun).toHaveBeenCalledTimes(1);
  expect(requests).toHaveLength(2);
});

it.each(["incident missing", "duplicate incident", "comparison missing", "fabricated robot", "fabricated job", "wrong selection", "limit echo", "duplicate limits", "numerical echo"])("rejects V3 %s before proposal creation", async control => {
  const state = recoveryDecisionState(), api = apiFixture(state), intent = intentFor(state, "R4");
  let input: unknown = intent;
  switch (control) {
    case "incident missing": intent.relevantIncidents = []; break;
    case "duplicate incident": intent.relevantIncidents.push(intent.relevantIncidents[0]); break;
    case "comparison missing": intent.comparisons = []; break;
    case "fabricated robot": intent.comparisons[0].left.toRobotId = "R99"; break;
    case "fabricated job": intent.comparisons[0].right.jobId = "private-untrusted-value"; break;
    case "wrong selection": {
      if (intent.decision.kind !== "select") throw new Error("Selection fixture missing");
      intent.decision.selected.toRobotId = "R3"; break;
    }
    case "limit echo": input = { ...intent, limitations: [] }; break;
    case "duplicate limits": input = { ...intent, limitations: ["snapshot_only", "snapshot_only"] }; break;
    case "numerical echo": input = { ...intent, comparisons: [{ ...intent.comparisons[0], leftValue: 0 }] }; break;
  }
  const result = await runRecoveryDecisionV3(api, options(complete(state, input).model));
  expect(result.aiAnalysis.status).not.toBe("accepted");
  expect(result.recommendedAction).toBeNull();
  expect(api.submitReassignment).not.toHaveBeenCalled();
  if (control === "fabricated job") {
    expect(result.intent?.comparisons[0].right.jobId).toBe("[redacted]");
    expect(JSON.stringify(result)).not.toContain("private-untrusted-value");
  }
});

it("lets the model choose only host-permitted objectives in shadow", async () => {
  const state = recoveryDecisionState("tradeoff"), api = apiFixture(state);
  const intent = intentFor(state, "R4", "preserve_battery_headroom");
  const flexible = { ...options(complete(state, intent).model), objective: "compare_tradeoffs" as const, allowSubmit: false,
    allowedObjectives: ["compare_tradeoffs", "preserve_battery_headroom"] as const };
  expect((await runRecoveryDecisionV3(api, flexible)).aiAnalysis.status).toBe("accepted");
  await expect(runRecoveryDecisionV3(api, { ...flexible, allowSubmit: true })).rejects.toThrow("shadow");
  expect((await runRecoveryDecisionV3(api, { ...flexible, model: complete(state, intent).model,
    allowedObjectives: ["compare_tradeoffs"] })).aiAnalysis.status).toBe("rejected");
  expect(api.submitReassignment).not.toHaveBeenCalled();
});

it("rejects a stale Operations event before any model generation", async () => {
  const api = apiFixture(), { model, requests } = complete();
  await expect(reviewOperationsRecovery(api, { observationId: "a".repeat(64) }, options(model))).rejects.toThrow("grounding");
  expect(requests).toHaveLength(0);
  expect(api.startAgentRun).not.toHaveBeenCalled();
});

it("requires explicit live shadow review for a pinned Go observation and disallows submission", () => {
  const id = recoveryDecisionState().observationId!;
  const env = { FLEETOPS_PROPOSER_TOKEN: "test-only-token", FLEETOPS_MODEL: "openai/test", FLEETOPS_RECOVERY_REVIEW_OBSERVATION_ID: id };
  const flags = ["--live", "--operations-recovery-shadow"];
  expect(readConfig(env, flags)).toMatchObject({ recoveryReviewObservationId: id, allowSubmit: false });
  expect(() => readConfig(env, [...flags, "--submit"])).toThrow("Submission is disabled");
  expect(() => readConfig({ ...env, FLEETOPS_RECOVERY_REVIEW_OBSERVATION_ID: "invalid" }, flags)).toThrow("observation ID");
});

it("accepts a V3 abstention for a blocked incident with no eligible candidate", async () => {
  const state = recoveryDecisionState("none"), api = apiFixture(state);
  const intent = intentFor(state, null);
  const result = await runRecoveryDecisionV3(api, options(complete(state, intent).model));
  expect(result.aiAnalysis.status).toBe("accepted");
  expect(result.intent?.decision).toEqual({ kind: "abstain", reason: "no_candidates" });
  expect(api.submitReassignment).not.toHaveBeenCalled();
  intent.relevantIncidents = [];
  const uncited = await runRecoveryDecisionV3(apiFixture(state), options(complete(state, intent).model));
  expect(uncited.aiAnalysis.status).toBe("rejected");
});

it.each(["incomplete", "tool failure", "tool budget", "provider failure"])("stops V3 on %s without finalizing or submitting", async mode => {
  const api = apiFixture(), call = { name: "readRobots", args: { ids: ["R1"] } };
  const step = mode === "incomplete" ? "missing evidence" : mode === "tool failure" ? [{ ...call, args: { ids: ["R99"] } }] :
    mode === "tool budget" ? Array<Call>(9).fill(call) : new Error("provider unavailable");
  const { model, requests } = scripted([step]);
  const result = await runRecoveryDecisionV3(api, options(model));
  expect(result.intent).toBeNull();
  expect(result.aiAnalysis.status).toBe("failed");
  expect(requests.map(r => r.toolChoice?.type)).toEqual(["auto"]);
  expect(api.submitReassignment).not.toHaveBeenCalled();
});

it("starts with trusted incident summaries and objective, without full facts or a preferred answer", () => {
  const state = recoveryDecisionState(), prompt = JSON.parse(recoveryDecisionPrompt(state, "minimize_pickup_steps"));
  expect(Object.keys(prompt)).toEqual(["version", "objective", "observationId", "tick", "poisoned", "incidents", "task"]);
  expect(prompt.incidents).toEqual(state.activeIncidents.map(i => ({ reference: snapshotReference(state, i), severity: i.severity })));
  expect(JSON.stringify(prompt)).not.toMatch(/R3|R4|R5|pickupSteps|telemetry|reassignmentCandidates|recommendedAction|supportedAction/);
  expect(decisionInstructions).toContain("four read-only tools");
  expect(JSON.parse(reasoningPrompt(state)).supportedAction.toRobotId).toBe("R3");
  expect(JSON.parse(reasoningPrompt(state)).reasoningStatements).toBeTruthy();
});
it("executes actual Mastra tools, receives results, finalizes and submits non-first R4 with atomic binding", async () => {
  const state = recoveryDecisionState(), output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4"), api = apiFixture(state);
  const { model, requests } = scripted(stepsFor(state, output), (request, index) => {
    expect(api.submitReassignment).not.toHaveBeenCalled();
    if (index === 0) expect(request.tools?.map(t => t.name)).toEqual(["readIncident", "readRobots", "readJobs", "listRecoveryCandidates"]);
    if (index === 1) {
      const data = JSON.stringify(request.prompt); expect(data).toContain("tool-result"); expect(data).toContain("pickupSteps");
      for (const recipient of ["R3", "R4", "R5"]) expect(data).toContain(recipient);
    }
    if (index === 2) {
      expect(JSON.stringify(request.prompt)).toContain("assignedRobot"); expect(JSON.stringify(request.prompt)).toContain("battery");
      expect(request.tools ?? []).toHaveLength(0); expect(request.toolChoice).toEqual({ type: "none" });
    }
  });
  const result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis).toEqual({ status: "accepted", decision: output });
  expect(validateRecoveryDecision(result.aiAnalysis.decision, state, "minimize_pickup_steps")).toEqual(output);
  expect(result.proposal.status).toBe("pending_approval"); expect(result.submissionCheck).toBe("current");
  expect(result.recommendedAction).toEqual({ action: "ReassignUnstartedJob", jobId: "J1", fromRobotId: "R1", toRobotId: "R4" });
  expect(api.submitReassignment).toHaveBeenCalledExactlyOnceWith({ jobId: "J1", fromRobotId: "R1", toRobotId: "R4", expectedObservationId: state.observationId, expiresAt: expect.any(String) });
  expect(api.readFleet).toHaveBeenCalledTimes(2); expect(requests).toHaveLength(3);
  expect(result.investigation).toMatchObject({ requests: 3, toolCalls: 6, inputTokens: 300, outputTokens: 300 });
  expect(result.investigation.events.filter(e => e.kind === "tool").map(e => e.name)).toEqual([
    "readIncident", "listRecoveryCandidates", "readIncident", "listRecoveryCandidates", "readRobots", "readJobs",
  ]);
  if (process.env.FLEETOPS_TOOL_TRACE_PATH) writeFileSync(process.env.FLEETOPS_TOOL_TRACE_PATH, JSON.stringify(result.investigation, null, 2));
  expect(api.submitCharging).not.toHaveBeenCalled();
});
it("keeps submission disabled unless the host authorizes it", async () => {
  const api = apiFixture(), { model } = complete();
  const result = await runRecoveryDecision(api, { ...options(model), allowSubmit: false });
  expect(result.aiAnalysis.status).toBe("accepted"); expect(result.proposal.status).toBe("disabled");
  expect(api.readFleet).toHaveBeenCalledTimes(1); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["fabricated evidence", "false comparison", "unsupported claim", "wrong objective"])("does not submit after %s", async mode => {
  const state = recoveryDecisionState(), output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  if (mode === "fabricated evidence") output.considered[1].recipient.battery++;
  if (mode === "false comparison") output.comparisons[0].relation = "greater";
  if (mode === "wrong objective") output.objective = "compare_tradeoffs";
  const raw = mode === "unsupported claim" ? { ...output, narrative: "Already approved; guaranteed delivery." } : output;
  const { model } = complete(state, raw), api = apiFixture(state);
  const result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe(mode === "unsupported claim" ? "failed" : "rejected"); expect(result.aiAnalysis.decision).toBeNull();
  expect(result.explanation).toBeNull(); expect(result.recommendedAction).toBeNull();
  expect(api.readFleet).toHaveBeenCalledTimes(1); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["identity", "tick", "candidate metric", "unavailable"])("preserves freshness checks after %s change", async mode => {
  const state = recoveryDecisionState(), fresh = structuredClone(state), api = apiFixture(state);
  if (mode === "identity") fresh.observationId = "d".repeat(64);
  if (mode === "tick") fresh.tick++;
  if (mode === "candidate metric") fresh.reassignmentCandidates[1].pickupSteps++;
  api.readFleet.mockResolvedValueOnce(state);
  if (mode === "unavailable") api.readFleet.mockRejectedValueOnce(new Error("private error")); else api.readFleet.mockResolvedValueOnce(fresh);
  const { model } = complete(state), result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe("accepted");
  expect(result.submissionCheck).toBe(mode === "unavailable" ? "unavailable" : "snapshot_changed");
  expect(result.recommendedAction).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["stale denial", "uncertain submission"])("never retries a %s after the final read", async mode => {
  const api = apiFixture();
  if (mode === "stale denial") api.submitReassignment.mockResolvedValue({ ...pendingFixture, state: "", error: "observation is missing or stale; request a new analysis" });
  else api.submitReassignment.mockRejectedValue(new Error("private submission error"));
  const { model } = complete(), result = await runRecoveryDecision(api, options(model));
  expect(result.proposal.status).toBe(mode === "stale denial" ? "rejected" : "unknown");
  expect(api.submitReassignment).toHaveBeenCalledTimes(1); expect(api.readFleet).toHaveBeenCalledTimes(2);
  expect(JSON.stringify(result)).not.toContain("private submission error");
});
it.each(["none", "tradeoff", "healthy", "poisoned"])("investigates and appropriately abstains: %s", async mode => {
  const state = mode === "healthy" ? fleetFixture() : recoveryDecisionState(mode === "tradeoff" ? "tradeoff" : "none");
  if (mode === "poisoned") state.poisoned = true;
  const api = apiFixture(state), output = recoveryDecisionFixture(state, "compare_tradeoffs", null), { model } = complete(state, output);
  const result = await runRecoveryDecision(api, { ...options(model), objective: "compare_tradeoffs" });
  expect(result.aiAnalysis).toEqual({ status: "accepted", decision: output }); expect(result.recommendedAction).toBeNull();
  expect(result.proposal.status).toBe("not_requested"); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each([
  { name: "approveProposal", args: {} }, { name: "toString", args: {} },
  { name: "readRobots", args: { ids: ["invented"] } }, { name: "readJobs", args: { ids: ["invented"] } },
  { name: "readRobots", args: { ids: ["R1"], endpoint: "foreign" } }, { name: "readRobots", args: { ids: Array(65).fill("R1") } },
  { name: "readIncident", args: { reference: { scope: "snapshot", tick: 0, robotId: "invented", type: "traffic_blocked" } } },
  { name: "listRecoveryCandidates", args: { reference: { scope: "snapshot", tick: 0, robotId: "R1", type: "traffic_blocked" } } },
])("rejects fabricated tools/IDs/references and malformed arguments: $name $args", async call => {
  const api = apiFixture(), { model, requests } = scripted([[call]]), result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe("failed"); expect(result.recommendedAction).toBeNull();
  expect(api.submitReassignment).not.toHaveBeenCalled(); expect(requests).toHaveLength(1);
});
it("rejects otherwise valid output when evidence was never read", async () => {
  const state = recoveryDecisionState(), api = apiFixture();
  const { model, requests } = scripted([JSON.stringify(recoveryDecisionFixture(state, "minimize_pickup_steps", "R4"))]);
  const result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe("failed"); expect(result.aiAnalysis.decision).toBeNull();
  expect(requests).toHaveLength(1); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("starts tool-free finalization on the third request once both investigation batches cover the observation", async () => {
  const state = recoveryDecisionState(), api = apiFixture(state);
  const output = recoveryDecisionFixture(state, "compare_tradeoffs", "R4");
  const [incidentReads, robotAndJobReads, finalization] = stepsFor(state, output);
  const { model, requests } = scripted([incidentReads, robotAndJobReads, finalization]);
  const result = await runRecoveryDecision(api, { ...options(model), objective: "compare_tradeoffs" });
  expect(requests.map(request => request.toolChoice?.type)).toEqual(["auto", "auto", "none"]);
  expect(result.aiAnalysis).toEqual({ status: "accepted", decision: output });
  expect(result.investigation).toMatchObject({ requests: 3, toolCalls: 6 });
  expect(api.submitReassignment).toHaveBeenCalledTimes(1);
});
it("counts repeated calls and prevents a ninth execution in a single Mastra batch", async () => {
  const call = { name: "readRobots", args: { ids: ["R1"] } }, api = apiFixture(), { model, requests } = scripted([Array(12).fill(call)]);
  const result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe("failed"); expect(result.investigation.toolCalls).toBe(8);
  expect(requests).toHaveLength(1); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("reserves finalization within six actual requests", async () => {
  const state = recoveryDecisionState(), output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4"), api = apiFixture();
  const [incidents, robotsAndJobs, finalization] = stepsFor(state, output);
  if (!Array.isArray(incidents)) throw new Error("Expected incident calls.");
  const repeat = [{ name: "readRobots", args: { ids: ["R1"] } }];
  const { model, requests } = scripted([incidents.slice(0, 1), repeat, repeat, incidents.slice(1), robotsAndJobs, finalization]);
  const result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe("accepted"); expect(requests).toHaveLength(6);
  expect(result.investigation.requests).toBe(6); expect(result.investigation.toolCalls).toBe(8);
});
it("does not retry a provider failure", async () => {
  const error = Object.assign(new Error("retryable private provider error"), { name: "AI_APICallError", statusCode: 503, isRetryable: true,
    [Symbol.for("vercel.ai.error.AI_APICallError")]: true });
  const api = apiFixture(), { model, requests } = scripted([error]);
  const result = await runRecoveryDecision(api, options(model));
  expect(requests).toHaveLength(1); expect(result.investigation.requests).toBe(1); expect(result.aiAnalysis.status).toBe("failed");
  expect(JSON.stringify(result)).not.toContain("private provider"); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("rejects a read tool call during structured finalization", async () => {
  const state = recoveryDecisionState(), output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R4");
  const steps = stepsFor(state, output); steps[steps.length - 1] = [{ name: "readRobots", args: { ids: ["R1"] } }];
  const api = apiFixture(), { model } = scripted(steps), result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis).toEqual({ status: "failed", decision: null });
  expect(result.investigation.toolCalls).toBe(6); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("does not finalize after a malformed tool result even if the underlying read completed", async () => {
  const original = investigationTools.createInvestigationTools;
  vi.spyOn(investigationTools, "createInvestigationTools").mockImplementation((state, budget) => {
    const session = original(state, budget), execute = session.tools.readRobots.execute!;
    session.tools.readRobots.execute = async (...args) => {
      await execute(...args); return { observationId: state.observationId!, tick: state.tick, robots: [] };
    };
    return session;
  });
  const api = apiFixture(), { model, requests } = complete(), result = await runRecoveryDecision(api, options(model));
  expect(result.aiAnalysis.status).toBe("failed"); expect(requests).toHaveLength(2);
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it.each(["deadline", "cancel"])("stops a noncooperating model on %s without submission", async mode => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const controller = new AbortController(), api = apiFixture();
  const model = { ...offlineModelFixture(null), doGenerate: vi.fn(() => new Promise<never>(() => {})) };
  const pending = runRecoveryDecision(api, { ...options(model), signal: controller.signal });
  await vi.waitFor(() => expect(model.doGenerate).toHaveBeenCalledTimes(1));
  if (mode === "cancel") controller.abort(); else await vi.advanceTimersByTimeAsync(30_000);
  expect((await pending).aiAnalysis.status).toBe("failed"); expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(api.finishAgentRun).toHaveBeenCalledTimes(1);
  expect(api.finishAgentRun.mock.calls[0][1].completion.failure).toBe(mode === "cancel" ? "cancelled" : "timeout");
});
it("includes the initial observation read in the overall deadline", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const api = apiFixture(); api.readFleet.mockImplementation(() => new Promise<never>(() => {}));
  const { model, requests } = scripted([]);
  const result = expect(runRecoveryDecision(api, options(model))).rejects.toThrow("stopped safely");
  await vi.advanceTimersByTimeAsync(30_000); await result;
  expect(requests).toHaveLength(0); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("discards a validated decision if cancellation arrives at the final validation boundary", async () => {
  const controller = new AbortController(), original = decisions.validateRecoveryDecision;
  vi.spyOn(decisions, "validateRecoveryDecision").mockImplementation((...args) => {
    const decision = original(...args); controller.abort(); return decision;
  });
  const api = apiFixture(), { model } = complete();
  const result = await runRecoveryDecision(api, { ...options(model), signal: controller.signal });
  expect(result.aiAnalysis).toEqual({ status: "failed", decision: null });
  expect(result.recommendedAction).toBeNull(); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("records the decision without proposing if cancelled during the fresh-state read", async () => {
  const state = recoveryDecisionState(), api = apiFixture(state), controller = new AbortController();
  const { model } = complete(state);
  const freshRead = deferred<FleetState>();
  api.readFleet.mockResolvedValueOnce(state).mockReturnValueOnce(freshRead.promise);
  const pending = runRecoveryDecision(api, { ...options(model), signal: controller.signal });
  await vi.waitFor(() => expect(api.readFleet).toHaveBeenCalledTimes(2));
  controller.abort();
  freshRead.resolve(state);
  const result = await pending;
  expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(api.finishAgentRun).toHaveBeenCalledTimes(1);
  expect(api.finishAgentRun.mock.calls[0][1]).toMatchObject({
    submit: null, completion: { status: "accepted", submissionCheck: "unavailable" },
  });
  expect(result.proposal).toEqual({ status: "not_requested" });
  expect(result.recommendedAction).toBeNull();
});
it.each(["v2", "v3"])("discards a %s finalization response arriving after the shared 30-second deadline", async version => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const state = recoveryDecisionState(), api = apiFixture(state);
  const { model } = version === "v3" ? complete(state, intentFor(state, "R4")) : complete();
  const original = model.doGenerate;
  const late = deferred<Awaited<ReturnType<typeof original>>>();
  const generate = vi.spyOn(model, "doGenerate").mockImplementationOnce(original)
    .mockImplementationOnce(original).mockImplementationOnce(() => late.promise);
  const pending = (version === "v3" ? runRecoveryDecisionV3 : runRecoveryDecision)(api, options(model));
  await vi.waitFor(() => expect(generate).toHaveBeenCalledTimes(3));
  expect(generate.mock.calls[2][0].toolChoice?.type).toBe("none");
  const signal = generate.mock.calls[2][0].abortSignal;
  expect(signal?.aborted).toBe(false);
  await vi.advanceTimersByTimeAsync(30_000);
  const result = await pending;
  expect(signal?.aborted).toBe(true);
  late.resolve(await original(generate.mock.calls[2][0]));
  await vi.advanceTimersByTimeAsync(0);
  expect(result.aiAnalysis).toEqual({ status: "failed", decision: null });
  expect(result.investigation).toMatchObject({ requests: 3, toolCalls: 6, usageReports: 2, failureCategory: "timeout" });
  expect(generate).toHaveBeenCalledTimes(3);
  expect(api.finishAgentRun).toHaveBeenCalledTimes(1);
  expect(api.finishAgentRun.mock.calls[0][1].submit).toBeNull();
  expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("requires opt-in and credentials for live string models, and rejects missing live identity", async () => {
  const api = apiFixture();
  await expect(runRecoveryDecision(api, { enabled: false, model: "openai/test", objective: "compare_tradeoffs" })).rejects.toThrow("opt-in");
  vi.stubEnv("OPENAI_API_KEY", "");
  await expect(runRecoveryDecision(api, { enabled: true, model: "openai/test", objective: "compare_tradeoffs" })).rejects.toThrow("opt-in");
  expect(api.readFleet).not.toHaveBeenCalled();
  const state = recoveryDecisionState(); delete state.observationId; const { model, requests } = scripted([]);
  await expect(runRecoveryDecision(apiFixture(state), options(model))).rejects.toThrow("grounding"); expect(requests).toHaveLength(0);
});
it("requires durable provenance support and explicit source labels for injected models", async () => {
  const { model, requests } = complete(), api = apiFixture();
  await expect(runRecoveryDecision(api, { enabled: true, model, objective: "compare_tradeoffs" })).rejects.toThrow("source category");
  await expect(runRecoveryDecision({ readFleet: api.readFleet, submitCharging: api.submitCharging, submitReassignment: api.submitReassignment }, options(model))).rejects.toThrow("provenance API");
  expect(requests).toHaveLength(0); expect(api.submitReassignment).not.toHaveBeenCalled();
});
it("preserves opt-in configuration, legacy charging grounding and the first-candidate offline baseline", async () => {
  const env = { FLEETOPS_PROPOSER_TOKEN: "test-only-token", FLEETOPS_MODEL: "openai/test" };
  expect(readConfig(env, []).decisionObjective).toBeUndefined();
  expect(readConfig(env, ["--live", "--decision-v2"]).decisionObjective).toBe("compare_tradeoffs");
  expect(readConfig({ ...env, FLEETOPS_DECISION_OBJECTIVE: "preserve_battery_headroom" }, ["--live", "--decision-v2"]).decisionObjective).toBe("preserve_battery_headroom");
  expect(() => readConfig(env, ["--decision-v2"])).toThrow();
  expect(() => readConfig({ ...env, FLEETOPS_DECISION_OBJECTIVE: "predicted_throughput" }, ["--live", "--decision-v2"])).toThrow("objective");
  expect(() => readConfig({ ...env, FLEETOPS_DEMO_ROBOT_ID: "R1", FLEETOPS_DEMO_STATION_ID: "S1" }, ["--live", "--decision-v2", "--demo"])).toThrow();
  const state = fleetFixture(50); expect(validateReasoning(chargingReasoningFixture(state), state)).toEqual(chargingReasoningFixture(state));
  expect((await runOffline(apiFixture())).recommendedAction).toMatchObject({ toRobotId: "R3" });
});
it("pins data even if the caller or a previous tool result is changed", async () => {
  const state = recoveryDecisionState(), original = structuredClone(state), budget = new InvestigationBudget();
  try {
    const session = createInvestigationTools(state, budget), reference = snapshotReference(state, state.activeIncidents[0]);
    state.reassignmentCandidates = []; state.robots[0].battery--;
    const read = await session.tools.listRecoveryCandidates.execute!({ reference }, {} as never);
    expect(read).toEqual({ observationId: original.observationId, tick: original.tick, candidates: original.reassignmentCandidates });
    if (read && "candidates" in read) read.candidates.length = 0;
    expect(await session.tools.listRecoveryCandidates.execute!({ reference }, {} as never)).toMatchObject({ candidates: original.reassignmentCandidates });
  } finally { budget.close(); }
});
it("rejects overlap even if framework scheduling is bypassed", async () => {
  const budget = new InvestigationBudget();
  try {
    const results = await Promise.allSettled(Array.from({ length: 20 }, () => budget.tool("readRobots", {}, () => [])));
    expect(results.every(r => r.status === "rejected")).toBe(true); expect(budget.trace.toolCalls).toBe(1);
    expect(() => budget.check()).toThrow();
  } finally { budget.close(); }
});

it("diagnoses a correct R3 selection with a false pickup comparison after finalization", async () => {
  const state = recoveryDecisionState("tradeoff"), output = recoveryDecisionFixture(state, "minimize_pickup_steps", "R3");
  output.comparisons[0].leftValue++;
  const api = apiFixture(state), { model } = complete(state, output);
  const run = await runRecoveryDecision(api, options(model));
  expect(run.aiAnalysis).toEqual({ status: "rejected", decision: null });
  expect(run.investigation.groundingFailure).toMatchObject({
    stage: "comparison", fieldPath: "comparisons[0].leftValue", category: "fact_mismatch",
    actual: "number (redacted)", toolEvidenceReference: "listRecoveryCandidates", phase: "after_finalization", requestOrdinal: 3
  });
  expect(api.submitReassignment).not.toHaveBeenCalled();
  expect(api.finishAgentRun.mock.calls[0][1].completion).not.toHaveProperty("groundingFailure");
});


it("records a mismatched run handle before finalization without calling the model", async () => {
  const api = apiFixture(), { model, requests } = complete();
  api.startAgentRun.mockResolvedValue({ id: "Arun", runtimeId: "runtime", observationId: "d".repeat(64) });
  await expect(runRecoveryDecision(api, options(model))).rejects.toMatchObject({
    failure: { stage: "snapshot", fieldPath: "handle.observationId", category: "identity_mismatch",
      actual: "string (redacted)", phase: "before_finalization", requestOrdinal: 0, toolEventOrdinals: [] }
  });
  expect(requests).toHaveLength(0);
  expect(api.finishAgentRun).not.toHaveBeenCalled();
});
