import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mkdtempSync, writeFileSync, unlinkSync, rmdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { baseline, dataset, quality, syntheticState } from "./agent07-dataset.js";
import { offlineEvaluation, aggregate, replayProjection, syntheticAPI, trial } from "./agent07-runner.js";
import { scriptedModel, type Probe } from "./agent07-scripted.js";
import { bounds, permitLive, planHash, RequestReserve, sha256, type Config, type PriceReview } from "./agent07-cost.js";
import { agent07CLI, parseArgs, isolatedDatabase } from "./agent07-cli.js";
import { liveTransport } from "./agent07-network.js";
import { outcomeSummary, type Branch } from "./agent07-outcomes.js";

beforeEach(() => { vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network allowed in unit evaluations"); })); });
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers(); });
const scenario = (id: string) => dataset.scenarios.find(s => s.id === id)!;
it("declares independent versioned objectives, eligibility controls and fault schedules", () => {
  expect(dataset.version).toBe("agent07-v1"); expect(dataset.measurementFaults).toEqual([]);
  expect(new Set(dataset.scenarios.map(s => s.id)).size).toBe(12);
  expect(dataset.scenarios.filter(s => s.comparable)).toHaveLength(8);
});
it("evaluates independent baselines on controlled candidate order, minima, headroom and ties", () => {
  const s = syntheticState(scenario("non-first-pickup"));
  expect(baseline(s, "first-eligible")?.toRobotId).toBe("R3"); expect(baseline(s, "minimum-pickup")?.toRobotId).toBe("R4");
  s.reassignmentCandidates.reverse(); expect(baseline(s, "first-eligible")?.toRobotId).toBe("R5"); expect(baseline(s, "minimum-pickup")?.toRobotId).toBe("R4");
  const tie = syntheticState(scenario("tied-pickup"));
  for (const recipient of ["R3", "R4"]) expect(quality(scenario("tied-pickup"), tie, { jobId: "J1", fromRobotId: "R1", toRobotId: recipient })).toMatchObject({ objectiveMet: true, regret: 0 });
  const tradeoff = syntheticState(scenario("tradeoff-headroom")); expect(baseline(tradeoff, "maximum-headroom")?.toRobotId).toBe("R4"); expect(baseline(tradeoff, "minimum-pickup")?.toRobotId).toBe("R3");
});
it("does not invent a unique optimum or scalar regret for compare_tradeoffs", () => {
  const s = { ...scenario("tradeoff-pickup"), expected: "admitted", quality: "pareto-only" as const }, state = syntheticState(s);
  for (const c of state.reassignmentCandidates) expect(quality(s, state, c)).toMatchObject({ admitted: true, objectiveMet: true, regret: null, dominated: false });
});
it("runs real Mastra tools without answer hints or expected labels entering model context", async () => {
  const s = scenario("non-first-pickup"), state = syntheticState(s), script = scriptedModel();
  const row = await trial(s, "v2-investigation", syntheticAPI(state), { model: script.model });
  expect(row).toMatchObject({ status: "accepted", selected: { toRobotId: "R4" }, quality: { regret: 0 }, submissionAttempts: 0 });
  expect(row.completion).toMatchObject({ modelRequests: 4, toolExecutions: 6, reportedUsage: { totalTokens: 8 } });
  const first = JSON.stringify(script.requests[0].prompt); expect(first).not.toMatch(/expected|quality|supportedAction|recommendedAction|non-first-pickup|R4|scriptedResponse/);
  expect(row.completion!.steps.filter(s => s.kind === "tool").map(s => s.tool)).toEqual(["readIncident", "listRecoveryCandidates", "readIncident", "listRecoveryCandidates", "readRobots", "readJobs"]);
  expect(JSON.stringify(row)).not.toMatch(/tool-result|Private provider|ignore all/i);
});
it("keeps eligibility, objective quality and generation failures in separate denominators", async () => {
  const report = await offlineEvaluation();
  expect(report.actualLLM).toBe("not_measured"); expect(report.rows).toHaveLength(39);
  expect(report.rows.filter(r => r.policy === "v2-investigation")).toHaveLength(12);
  const normal = report.rows.find(r => r.scenarioId === "non-first-pickup" && r.policy === "first-eligible")!;
  expect(normal.quality).toMatchObject({ admitted: true, regret: 5, objectiveMet: false });
  const agg = report.aggregate.find(r => r.policy === "v2-investigation")!;
  expect(agg.attemptedTrials).toBe(12); expect(agg.groundingFailures).toBe(1); expect(agg.unsupportedToolAttempts).toBe(1);
  expect(agg.appropriateAbstention).toEqual({ numerator: 3, denominator: 3 }); expect(agg.objectiveMet).toEqual({ numerator: 6, denominator: 6 });
});
it("accepts a justified operator tradeoff abstention without inventing a uniquely optimal selection", async () => {
  const s = scenario("tradeoff-compare"), row = await trial(s,"v2-investigation",syntheticAPI(syntheticState(s)),{ probe:"abstain-tradeoff" });
  expect(row).toMatchObject({ status:"accepted", selected:null, abstention:"operator_tradeoff", expectationMet:true, quality:{ regret:null, objectiveMet:true }, submissionAttempts:0 });
});
it.each(["false-evidence", "invalid-tool", "model-failure", "malformed", "budget", "missing-usage", "untrusted-output"] as Probe[])("contains the %s failure without commands or submission retries", async probe => {
  const s = scenario("non-first-pickup"), state = syntheticState(s);
  const row = await trial(s, "v2-investigation", syntheticAPI(state), { probe, allowSubmit: true });
  expect(row.groundingAccepted).toBe(false); expect(row.selected).toBeNull(); expect(row.submissionAttempts).toBe(0);
  expect(row.proposal?.status).not.toBe("pending_approval"); expect(row.completion!.modelRequests).toBeLessThanOrEqual(6); expect(row.completion!.toolExecutions).toBeLessThanOrEqual(8);
  expect(row.completion!.modelRequests).toBe(["false-evidence","malformed","untrusted-output"].includes(probe)?4:1);
  expect(row.modelCalls.localAdapterFailures).toBe(probe==="model-failure"?1:0);
  expect(aggregate([row]).find(r => r.policy === "v2-investigation")!.attemptedTrials).toBe(1);
  if (probe === "missing-usage") expect(row.completion!.reportedUsage).toEqual({ status: "unavailable", inputTokens: null, outputTokens: null, totalTokens: null });
});
it("marks lost completion unknown and clears executable recommendations, with exactly one finish", async () => {
  const s = scenario("non-first-pickup"), api = syntheticAPI(syntheticState(s), "unknown"), finish = vi.spyOn(api, "finishAgentRun");
  const row = await trial(s, "v2-investigation", api);
  expect(finish).toHaveBeenCalledTimes(1); expect(row.failure).toBe("completion_unknown"); expect(row.provenance?.status).toBe("unknown"); expect(row.submissionAttempts).toBe(0);
});
it("rejects a changed final snapshot before submission and never retries", async () => {
  const s = scenario("stale"), api = syntheticAPI(syntheticState(s), "stale"), finish = vi.spyOn(api, "finishAgentRun");
  const row = await trial(s, "v2-investigation", api, { allowSubmit: true });
  expect(row).toMatchObject({ failure: "snapshot_changed", expectationMet: true, submissionAttempts: 0 }); expect(finish).toHaveBeenCalledTimes(1); expect(finish.mock.calls[0][1].submit).toBeNull();
});
it("retains uncertain baseline submissions without retrying or manufacturing approval", async () => {
  const s=scenario("non-first-pickup"), api=syntheticAPI(syntheticState(s)), submit=vi.spyOn(api,"submitReassignment");
  const row=await trial(s,"first-eligible",api,{allowSubmit:true});
  expect(row).toMatchObject({status:"generation-failed",failure:"submission_unknown",proposal:{status:"unknown"},submissionAttempts:1}); expect(submit).toHaveBeenCalledOnce();
});
it("rejects provider configurations and mislabeled mock/live inputs before any model request", async () => {
  const s=scenario("non-first-pickup"), api=syntheticAPI(syntheticState(s));
  expect((await trial(s,"v2-investigation",api,{model:"openai/unreviewed"})).status).toBe("generation-failed");
  const scripted=scriptedModel(); const row=await trial(s,"v2-investigation",api,{model:scripted.model,live:true,go:true});
  expect(row.status).toBe("generation-failed"); expect(scripted.requests).toHaveLength(0);
});
it("replays the scientific projection deterministically without regenerating provider calls", async () => {
  const cases = [scenario("non-first-pickup"), scenario("healthy")];
  expect(replayProjection((await offlineEvaluation(cases)).rows)).toEqual(replayProjection((await offlineEvaluation(cases)).rows));
});
it("keeps cancellation and elapsed-stop trials distinct from attempted generations", async () => {
  const s=scenario("non-first-pickup"), api=syntheticAPI(syntheticState(s)), start=vi.spyOn(api,"startAgentRun"), controller=new AbortController(); controller.abort();
  const row=await trial(s,"v2-investigation",api,{ signal:controller.signal, allowSubmit:true });
  expect(row).toMatchObject({status:"generation-failed",submissionAttempts:0,selected:null}); expect(start).not.toHaveBeenCalled();
  const stopped=await offlineEvaluation([s],1,0); expect(stopped).toMatchObject({plannedTrials:4,notAttemptedTrials:4,stoppedReason:"elapsed ceiling; no retry",rows:[]});
});
it("reports real paired units, censored controls and policy comparisons without manufacturing speedups", async () => {
  const s=scenario("non-first-pickup");
  const decision=await trial(s,"first-eligible",syntheticAPI(syntheticState(s)));
  const common={scenarioId:"non-first-pickup",trial:1,initialSimulatorStateSHA256:"identical",goValidation:"pending_admitted",serverVerifiedDecision:null,successfulReassignments:1,interventionFailures:0};
  const outcome={startTick:42,endTick:242,completedDeliveries:1,completionTick:50,unresolvedJobs:0,blockedRobotSamples:0,aggregateBatteryPercentagePoints:446};
  const rows: Branch[]=[{...common,policy:"no-intervention",decision:null,successfulReassignments:0,simulatorOutcome:{...outcome,completedDeliveries:0,completionTick:null,unresolvedJobs:1,blockedRobotSamples:200}},
    {...common,policy:"first-eligible",decision,simulatorOutcome:outcome},
    {...common,policy:"v2-investigation",decision:{...decision,policy:"v2-investigation"},simulatorOutcome:{...outcome,completionTick:45,aggregateBatteryPercentagePoints:451}}];
  const report=outcomeSummary(rows), effects=report.comparisons[0].effects!;
  expect(effects.find(e=>e.against==="first-eligible")).toMatchObject({completionTicksSaved:5,completedDeliveryDelta:0,finalStoredBatteryPercentagePointDelta:5});
  expect(effects.find(e=>e.against==="no-intervention")).toMatchObject({completionTicksSaved:null,completedDeliveryDelta:1,blockedRobotSampleDelta:-200});
  rows[1].initialSimulatorStateSHA256="different"; expect(outcomeSummary(rows).comparisons[0]).toMatchObject({comparable:false,effects:null});
});
const now = Date.parse("2026-10-18T12:00:00Z");
function reviewed() {
  const config: Config = { live: true, plan: false, go: true, scenarios: ["non-first-pickup"], trials: 2, provider: "openai", model: "reviewed-test-snapshot", maxRequests: 12, maxOutputTokens: 100, maxElapsedMs: 10000, budgetUSD: 1, sourceRevision: "a".repeat(40) };
  const review: PriceReview = { version: "agent07-price-v1", provider: "openai", model: config.model!, source: "https://developers.openai.com/api/docs/pricing", modelSource: "https://developers.openai.com/api/docs/models/reviewed-test-snapshot", reviewedAt: "2026-10-18T11:00:00Z", expiresAt: "2026-10-19T11:00:00Z", acknowledged: true, immutableModelAcknowledged: true,
    contextWindowTokens: 1000, modelMaxOutputTokens: 100, inputUSDPerMillion: 1, outputUSDPerMillion: 2, ancillaryUSDPerRequest: 0.001, highestApplicableTierAcknowledged: true, outputCapIncludesReasoning: true, responsesOutputCapSupported: true, allFeesIncludedAcknowledged: true };
  const raw = JSON.stringify(review); config.approveCostSHA256 = sha256(raw); return { config, review, raw };
}
it("reserves six full-context calls per investigation, all output/reasoning and ancillary fees", () => {
  const { config, raw } = reviewed(), p = bounds(config, raw, now);
  expect(p).toMatchObject({ maximumModelRequests: 12, maximumInputTokens: 12000, maximumOutputTokens: 1200, estimatedMaximumUSD: 0.0264 });
  const h = planHash(config, p, "dataset", "source"); expect(planHash({ ...config, model: "other" }, p, "dataset", "source")).not.toBe(h);
  const approvals=bounds({...config,testHarnessApprovals:true},raw,now); expect(planHash({...config,testHarnessApprovals:true},approvals,"dataset","source")).not.toBe(h);
  expect(() => permitLive({ ...config, approvePlanSHA256: h }, p, h, { CI: "1", OPENAI_API_KEY: "unit-placeholder" })).toThrow();
  expect(() => permitLive({ ...config, approvePlanSHA256: "wrong" }, p, h, { OPENAI_API_KEY: "unit-placeholder" })).toThrow();
});
it("rejects absent, stale, mismatched, unsupported-fee and over-budget reviews", () => {
  const { config, raw, review } = reviewed(); expect(() => bounds(config, undefined, now)).toThrow();
  expect(() => bounds({ ...config, approveCostSHA256: "wrong" }, raw, now)).toThrow();
  expect(() => bounds({ ...config, model: "other" }, raw, now)).toThrow(); expect(() => bounds({ ...config, budgetUSD: 0.001 }, raw, now)).toThrow();
  for (const changed of [{ ...review, expiresAt: "2026-10-17T12:00:00Z" }, { ...review, outputCapIncludesReasoning: false }, { ...review, allFeesIncludedAcknowledged: false }, { ...review, contextWindowTokens: 0 }]) {
    const bad = JSON.stringify(changed); expect(() => bounds({ ...config, approveCostSHA256: sha256(bad) }, bad, now)).toThrow();
  }
});
it("keeps failed/unknown reservations consumed, blocking transport retries and altered requests", async () => {
  const { config } = reviewed(), reserve = new RequestReserve(1, 0.1, 0.1, Date.now()+10000), network = vi.fn().mockRejectedValue(new Error("uncertain"));
  const transport = liveTransport(config, "http://127.0.0.1:1/graphql", reserve, network, new AbortController().signal, 1000);
  const init = { method: "POST", body: JSON.stringify({ model: config.model, max_output_tokens: 100, store: false, service_tier: "default" }) };
  transport.permit(); await expect(transport.fetch("https://api.openai.com/v1/responses", init)).rejects.toThrow();
  await expect(transport.fetch("https://api.openai.com/v1/responses", init)).rejects.toThrow(); expect(network).toHaveBeenCalledTimes(1); expect(reserve.calls).toBe(1);
  expect(() => transport.permit()).toThrow(); expect(() => reserve.take()).toThrow();
  const pristine = liveTransport(config, "http://127.0.0.1:1/graphql", new RequestReserve(1, 1, 1, Date.now()+10000), network, new AbortController().signal, 1000);
  pristine.permit(); await expect(pristine.fetch("https://api.openai.com/v1/responses", { ...init, body: JSON.stringify({ model: "other", max_output_tokens: 100 }) })).rejects.toThrow(); expect(network).toHaveBeenCalledTimes(1);
});
it("defaults to offline, plans without credentials/network, and rejects ambiguous or excessive CLI requests", async () => {
  expect(parseArgs([])).toMatchObject({ live: false, model: null, provider: null, trials: 1 });
  const out = vi.spyOn(process.stdout, "write").mockReturnValue(true); await agent07CLI(["--plan"]);
  expect(JSON.parse(String(out.mock.calls[0][0]))).toMatchObject({ providerRequests: 0, requestedModel: null, estimatedMaximumUSD: null });
  expect(() => parseArgs(["--live"])).toThrow(); expect(() => parseArgs(["--plan", "--plan"])).toThrow(); expect(() => parseArgs(["--live", "--offline"])).toThrow(); expect(() => parseArgs(["--scenario", "invented"])).toThrow();
  expect(()=>parseArgs(["--model","unreviewed"])).toThrow(); expect(()=>parseArgs(["--source-revision","short"])).toThrow();
  expect(() => bounds({ ...parseArgs([]), trials: 4 })).toThrow(); expect(() => bounds({ ...parseArgs([]), maxRequests: 1 })).toThrow();
});
it("requires a named localhost test database and keeps optional abstentions in their denominator", async () => {
  expect(isolatedDatabase("postgres://postgres@127.0.0.1:5432/fleetops_agent07?sslmode=disable")).toContain("fleetops_agent07");
  for(const value of [undefined,"postgres://localhost/fleetops","postgres://remote.example/fleetops_agent07"]) expect(()=>isolatedDatabase(value)).toThrow();
  const s=scenario("tradeoff-compare"), row=await trial(s,"v2-investigation",syntheticAPI(syntheticState(s)),{probe:"abstain-tradeoff"});
  expect(aggregate([row]).find(r=>r.policy==="v2-investigation")!.appropriateAbstention).toEqual({numerator:1,denominator:1});
});
it("produces a reviewed LIVE --plan in CI with no provider key, database, model resolution or inference", async () => {
  vi.useFakeTimers(); vi.setSystemTime(now);
  const {config,raw}=reviewed(), directory=mkdtempSync(join(tmpdir(),"fleetops-agent07-review-")), path=join(directory,"review.json"); writeFileSync(path,raw);
  const out=vi.spyOn(process.stdout,"write").mockReturnValue(true);
  try {
    await agent07CLI(["--live","--go","--plan","--scenario",config.scenarios[0],"--provider","openai","--model",config.model!,"--trials","2","--max-output-tokens","100","--source-revision",config.sourceRevision!,"--budget-usd","1","--cost-review",path,"--approve-cost-sha256",config.approveCostSHA256!]);
    expect(JSON.parse(String(out.mock.calls[0][0]))).toMatchObject({mode:"live-plan-only",maximumModelRequests:12,maximumOutputTokens:1200,estimatedMaximumUSD:0.0264,proposalSubmission:false,testHarnessApprovals:false});
  } finally { unlinkSync(path); rmdirSync(directory); }
});
it("blocks unsupported tools, oversized context and altered output limits before any provider request", async () => {
  const {config}=reviewed(), network=vi.fn().mockResolvedValue(new Response("{}")), body={model:config.model,max_output_tokens:100,store:false,service_tier:"default"};
  for(const altered of [{...body,max_output_tokens:101},{...body,tools:[{type:"web_search_preview"}]},{...body,input:"x".repeat(1000)}]) {
    const transport=liveTransport(config,"http://127.0.0.1:1/graphql",new RequestReserve(1,1,1,Date.now()+1000),network,new AbortController().signal,1000);
    transport.permit(); await expect(transport.fetch("https://api.openai.com/v1/responses",{method:"POST",body:JSON.stringify(altered)})).rejects.toThrow();
  }
  expect(network).not.toHaveBeenCalled();
  const transport=liveTransport(config,"http://127.0.0.1:1/graphql",new RequestReserve(1,1,1,Date.now()+1000),network,new AbortController().signal,1000);
  transport.permit(); await transport.fetch("https://api.openai.com/v1/responses",{method:"POST",body:JSON.stringify(body)});
  expect(transport.measurements).toEqual({providerHTTPAttempts:1,failedHTTPResponses:0,uncertainHTTPResults:0}); expect(network).toHaveBeenCalledOnce();
});
