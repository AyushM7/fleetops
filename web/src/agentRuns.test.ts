import { afterEach, expect, it, vi } from "vitest";
import { AgentDataError, parseAgentRecord } from "./agentRecords";
import { fetchAgentRuns, pollAgentRuns } from "./agentRuns";
import { activityFixture, agentFixture, outerFixture, responseFixture } from "./agentFixtures";

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });
const response = (body: unknown = responseFixture()) => new Response(JSON.stringify(body));
it("parses actual agent-run-v1 DTO shape and performs only bounded authenticated GraphQL reads", async () => {
  expect(parseAgentRecord(outerFixture())).toEqual(agentFixture());
  const request = vi.fn().mockResolvedValue(response()); vi.stubGlobal("fetch", request);
  const result = await fetchAgentRuns("private-view-token", new AbortController().signal);
  expect(result.runs).toEqual([agentFixture()]); expect(result.runtimeId).toBe(agentFixture().runtimeId);
  const [url, options] = request.mock.calls[0]; expect(url).toBe("/graphql"); expect(options.headers.Authorization).toBe("Bearer private-view-token");
  expect(options.credentials).toBe("omit"); expect(options.cache).toBe("no-store"); expect(options.redirect).toBe("error");
  const body = JSON.parse(options.body); expect(body.variables).toEqual({ limit: 10 });
  expect(body.query).toContain("agentRuns"); expect(body.query).not.toMatch(/mutation|startAgentRun|finishAgentRun|approveProposal|openai/);
});
it.each([
  (r: Record<string, unknown>) => { r.observationId = "f".repeat(64); },
  (r: Record<string, unknown>) => { r.runtimeId = "foreignRuntime"; },
  (r: Record<string, unknown>) => { r.id = "otherRun"; },
  (r: Record<string, unknown>) => { r.data = "{"; },
  (r: Record<string, unknown>) => { r.data = "x".repeat(100_000); },
  (r: Record<string, unknown>) => { (r.proposal as Record<string, unknown>).currentRuntime = false; },
  (r: Record<string, unknown>) => { (r.proposal as Record<string, unknown>).toRobotId = "R4"; },
  (r: Record<string, unknown>) => { (r.proposal as Record<string, unknown>).id = "unrelatedProposal"; },
  (r: Record<string, unknown>) => { r.proposal = null; },
])("rejects malformed outer identities and forged proposal associations: %s", alter => {
  const outer = outerFixture(); alter(outer); expect(() => parseAgentRecord(outer)).toThrow(AgentDataError);
});
it.each([
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedMetadata.version = "agent-run-v99" as never; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedMetadata.requestedModel = "openai/sk-private"; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedMetadata.reportedSource = "verified-live-openai" as never; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedCompletion!.decision!.comparisons[0].leftValue++; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedCompletion!.decision!.selected!.toRobotId = "fabricated"; },
  (r: ReturnType<typeof agentFixture>) => { r.serverVerifiedObservation.candidates[0].metrics.pickupSteps = -1; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedCompletion!.steps[0].tool = "approveProposal"; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedCompletion!.modelRequests = 7; },
  (r: ReturnType<typeof agentFixture>) => { r.agentReportedCompletion!.reportedUsage.totalTokens = null; },
  (r: ReturnType<typeof agentFixture>) => { r.completedAt = null; },
  (r: ReturnType<typeof agentFixture>) => { r.estimatedCostStatus = "free" as never; },
])("rejects unsupported or ungrounded inner data: %s", alter => {
  const run = agentFixture(); alter(run); expect(() => parseAgentRecord(outerFixture(run))).toThrow(AgentDataError);
});
it("rejects raw prompts, private outputs and extra generated strings instead of rendering them", () => {
  for (const field of ["prompt", "apiKey", "toolOutputs", "narrative"]) {
    const outer = outerFixture(); outer.data = JSON.stringify({ ...JSON.parse(outer.data), [field]: "private-secret" });
    expect(() => parseAgentRecord(outer)).toThrow("Unsupported or malformed");
  }
});
it("accepts missing server-verification flag as explicitly unconfirmed and absent metrics as unavailable", () => {
  const run = agentFixture(); run.serverVerifiedObservation.candidates[0].metrics.requiredBattery = undefined;
  const outer = outerFixture(run), data = JSON.parse(outer.data); delete data.serverVerifiedDecision; outer.data = JSON.stringify(data);
  expect(parseAgentRecord(outer).serverVerifiedDecision).toBeNull();
  expect(parseAgentRecord(outer).serverVerifiedObservation.candidates[0].metrics.requiredBattery).toBeUndefined();
});
it.each([
  { status: 401, body: {}, kind: "authentication" }, { status: 403, body: {}, kind: "forbidden" }, { status: 503, body: {}, kind: "unavailable" },
  { status: 200, body: { errors: [{ message: "private-secret", extensions: { code: "FORBIDDEN" } }] }, kind: "forbidden" },
  { status: 200, body: { errors: [{ message: "private-secret", extensions: { code: "UNAVAILABLE" } }] }, kind: "unavailable" },
  { status: 200, body: { errors: [{ extensions: { code: "AGENT_PROVENANCE_DENIED" } }] }, kind: "denied" },
])("separates access/outage outcomes and never forwards raw errors: $kind $status", async ({ status, body, kind }) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status })));
  await expect(fetchAgentRuns("private-token", new AbortController().signal)).rejects.toMatchObject({ kind, message: "Agent provenance read failed." });
});
it("bounds lists, rejects mixed runtimes, and detects a prior run beyond the current runtime tick", async () => {
  const old = outerFixture(), fresh = outerFixture(); fresh.runtimeId = "newRuntime"; fresh.id = "newRun";
  const freshData = JSON.parse(fresh.data); freshData.runtimeId = fresh.runtimeId; freshData.id = fresh.id; fresh.data = JSON.stringify(freshData);
  for (const body of [responseFixture(Array(11).fill(old)), responseFixture([old, old]), responseFixture([old, fresh]),
    { data: { agentRuns: [old], fleetState: { ...activityFixture().current!, tick: 0 } } }]) {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(body)));
    await expect(fetchAgentRuns("token", new AbortController().signal)).rejects.toThrow(AgentDataError);
  }
});
it("invalidates in-flight reads on disconnect, restart and superseding refresh, even if transport ignores cancellation", async () => {
  vi.useFakeTimers(); let late: ((r: Response) => void) | undefined;
  const request = vi.fn().mockImplementationOnce(() => new Promise<Response>(resolve => { late = resolve; })).mockResolvedValueOnce(response(responseFixture([])));
  vi.stubGlobal("fetch", request); const emit = vi.fn(), unauthorized = vi.fn();
  const old = pollAgentRuns("old-generation", emit, unauthorized); await vi.advanceTimersByTimeAsync(0); old.stop();
  const fresh = pollAgentRuns("new-generation", emit, unauthorized); await vi.advanceTimersByTimeAsync(0);
  late?.(response()); await vi.advanceTimersByTimeAsync(0);
  expect(request.mock.calls[0][1].signal.aborted).toBe(true);
  expect(emit.mock.calls.at(-1)?.[0]).toMatchObject({ phase: "ready", runs: [], runtimeId: null });
  expect(emit.mock.calls.filter(([feed]) => feed.runs.length)).toHaveLength(0); fresh.stop();
  await vi.advanceTimersByTimeAsync(10_000); expect(request).toHaveBeenCalledTimes(2);
});
it("manual refresh supersedes an older response and unavailable refresh clears prior records", async () => {
  vi.useFakeTimers(); let late: ((r: Response) => void) | undefined;
  const request = vi.fn().mockResolvedValueOnce(response()).mockImplementationOnce(() => new Promise<Response>(resolve => { late = resolve; })).mockRejectedValueOnce(new Error("private error"));
  vi.stubGlobal("fetch", request); const emit = vi.fn(), p = pollAgentRuns("token", emit, vi.fn());
  await vi.advanceTimersByTimeAsync(0); expect(emit.mock.calls.at(-1)?.[0].runs).toHaveLength(1);
  p.refresh(); p.refresh(); await vi.advanceTimersByTimeAsync(0); late?.(response()); await vi.advanceTimersByTimeAsync(0);
  expect(emit.mock.calls.at(-1)?.[0]).toMatchObject({ phase: "unavailable", runs: [] }); p.stop();
});
it("does not fabricate local demo runs or issue inference, provenance, or mutation calls on demo mount", () => {
  vi.stubGlobal("document", { querySelector: () => ({ getAttribute: () => "enabled" }) }); const request = vi.fn(); vi.stubGlobal("fetch", request);
  const emit = vi.fn(), p = pollAgentRuns("generation", emit, vi.fn()); expect(emit).toHaveBeenCalledWith(expect.objectContaining({ phase: "demo", runs: [] }));
  p.refresh(); p.stop(); expect(request).not.toHaveBeenCalled();
});
it("stops on viewer denial and authentication expiry without exposing earlier records", async () => {
  vi.useFakeTimers(); const unauthorized = vi.fn(), emit = vi.fn();
  const request = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ errors: [{ extensions: { code: "FORBIDDEN" } }] }))).mockResolvedValueOnce(new Response("expired", { status: 401 }));
  vi.stubGlobal("fetch", request); const denied = pollAgentRuns("viewer", emit, unauthorized);
  await vi.advanceTimersByTimeAsync(5000); expect(request).toHaveBeenCalledTimes(1); expect(emit.mock.calls.at(-1)?.[0].phase).toBe("forbidden"); denied.stop();
  const expired = pollAgentRuns("expired", emit, unauthorized); await vi.advanceTimersByTimeAsync(0); expect(unauthorized).toHaveBeenCalledOnce(); expired.stop();
});
