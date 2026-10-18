import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { createFleetClient } from "../../agent/src/graphql.js";
import { resolveEvaluationModel } from "../../agent/src/evaluation-investigation-model.js";
import { bounds, permitLive, planHash, RequestReserve, sha256, type Config } from "./agent07-cost.js";
import { dataset, datasetSHA256, policies, type Policy } from "./agent07-dataset.js";
import { aggregate, offlineEvaluation, trial, type Trial } from "./agent07-runner.js";
import { liveTransport } from "./agent07-network.js";
import { outcomeSummary, type Branch } from "./agent07-outcomes.js";

const root = fileURLToPath(new URL("../../", import.meta.url));
const sourceFiles = ["evals/src/agent07-cli.ts", "evals/src/agent07-cost.ts", "evals/src/agent07-dataset.ts", "evals/src/agent07-scripted.ts", "evals/src/agent07-network.ts", "evals/src/agent07-runner.ts", "evals/src/agent07-outcomes.ts",
  "evals/scripts/agent07.mjs", "agent/package-lock.json", "backend/go.mod", "backend/go.sum",
  "agent/src/decision-agent.ts", "agent/src/decision.ts", "agent/src/investigation-budget.ts", "agent/src/investigation-tools.ts", "agent/src/agent-run.ts", "agent/src/evaluation-investigation-model.ts",
  "agent/src/graphql.ts", "agent/src/submission.ts", "agent/src/contracts.ts", "agent/src/decision-contracts.ts", "backend/fleet/service.go", "backend/fleet/read.go", "backend/fleet/agent_runs.go", "backend/fleet/decision.go", "backend/sim/reassignment_fixture.go",
  "backend/sim/agent07_fixture.go", "backend/fleet/agent07_integration_test.go", "backend/fleet/agent07_export_test.go"];
export const sourceDigest = () => sha256(sourceFiles.map(path => `${path}\n${readFileSync(resolve(root, path), "utf8").replaceAll("\r\n", "\n")}`).join("\n"));
export function isolatedDatabase(value: string | undefined): string {
  try {
    const url=new URL(value??"");
    if (!["postgres:","postgresql:"].includes(url.protocol) || !["127.0.0.1","localhost","[::1]"].includes(url.hostname) || !/^\/fleetops_(agent07|test)[A-Za-z0-9_]*$/.test(url.pathname)) throw new Error();
    return value!;
  } catch { throw new Error("Explicit localhost fleetops_agent07/test database required; no production database fallback."); }
}
export function parseArgs(args: string[]): Config & { output?: string } {
  const flags = new Set(["--live", "--offline", "--plan", "--go", "--test-harness-approvals"]), values = new Map<string, string>();
  const allowed = new Set(["--scenario", "--trials", "--provider", "--model", "--max-requests", "--max-output-tokens", "--max-elapsed-ms", "--budget-usd", "--source-revision", "--cost-review", "--approve-cost-sha256", "--approve-plan-sha256", "--output"]);
  const seen = new Set<string>();
  for (let i = 0; i < args.length; i++) { const arg = args[i]; if (seen.has(arg) || !flags.has(arg) && !allowed.has(arg)) throw new Error("Unknown/duplicate agent07 option."); seen.add(arg);
    if (allowed.has(arg)) { const next = args[++i]; if (!next || next.startsWith("--")) throw new Error("Missing agent07 option value."); values.set(arg, next); } }
  if (seen.has("--live") && seen.has("--offline")) throw new Error("Conflicting modes.");
  const scenarios = values.get("--scenario")?.split(",") ?? dataset.scenarios.map(s => s.id);
  if (scenarios.some(id => !dataset.scenarios.some(s => s.id === id))) throw new Error("Unknown agent07 scenario.");
  const number = (name: string, fallback: number) => values.has(name) ? Number(values.get(name)) : fallback;
  const live = seen.has("--live");
  if (!live && ["--provider","--model","--cost-review","--approve-cost-sha256","--budget-usd","--approve-plan-sha256"].some(k=>values.has(k))) throw new Error("Provider/model/pricing options require --live (or --live --plan); offline always uses explicitly labeled scripted adapters.");
  if(values.has("--source-revision") && !/^[a-f0-9]{40}$/.test(values.get("--source-revision")!)) throw new Error("A full supplied base revision SHA is required.");
  if (live && (!values.has("--scenario") || !values.has("--model") || values.get("--provider") !== "openai")) throw new Error("Live mode requires explicit reviewed scenarios and exact OpenAI model; none is selected by default.");
  if (live && scenarios.some(id => dataset.scenarios.find(s => s.id === id)!.probe || id === "stale")) throw new Error("Injected negative probes and submission-race controls are offline-only, not live model-accuracy trials.");
  return { live, plan: seen.has("--plan"), go: seen.has("--go"), testHarnessApprovals:seen.has("--test-harness-approvals"), scenarios, trials: number("--trials", 1), model: values.get("--model") ?? null,
    provider: values.get("--provider") === "openai" ? "openai" : null, maxRequests: number("--max-requests", scenarios.length * number("--trials", 1) * 6),
    maxOutputTokens: number("--max-output-tokens", 6000), maxElapsedMs: number("--max-elapsed-ms", 600000), budgetUSD: values.has("--budget-usd") ? number("--budget-usd", 0) : null,
    sourceRevision: values.get("--source-revision") ?? null, costReview: values.get("--cost-review"), approveCostSHA256: values.get("--approve-cost-sha256"), approvePlanSHA256: values.get("--approve-plan-sha256"), output: values.get("--output") };
}
function reviewed(c: Config) {
  const p = bounds(c, c.live && c.costReview ? readFileSync(c.costReview, "utf8") : undefined);
  const digest = sourceDigest(), hash = planHash(c, p, datasetSHA256, digest);
  return { ...p, planSHA256: hash, datasetSHA256, sourceDigest: digest, sourceRevision: c.sourceRevision, selectedScenarios: c.scenarios,
    requestedModel: c.model, provider: c.provider, pricingCaveat: "Hash-reviewed operator acknowledgement, not a provider hard dollar cap. Actual billed cost requires authoritative evidence." };
}
export async function fixtureTrial(): Promise<void> {
  const config = JSON.parse(process.env.FLEETOPS_AGENT07_CONFIG ?? "null") as Config;
  if (!config || config.plan) throw new Error("Controlled Go fixture configuration missing.");
  const plan = reviewed(config);
  const scenario = dataset.scenarios.find(s => s.id === process.env.FLEETOPS_AGENT07_SCENARIO), policy = process.env.FLEETOPS_AGENT07_POLICY as Policy;
  if (!scenario || !config.scenarios.includes(scenario.id) || !policies.includes(policy) || !config.testHarnessApprovals && policy !== "v2-investigation") throw new Error("Unreviewed fixture trial.");
  const endpoint = new URL(process.env.FLEETOPS_AGENT07_ENDPOINT ?? "");
  if (endpoint.protocol !== "http:" || endpoint.hostname !== "127.0.0.1" || endpoint.pathname !== "/graphql" || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error("Isolated localhost Go endpoint required.");
  const expires = Date.parse(process.env.FLEETOPS_AGENT07_DEADLINE ?? ""); if (!Number.isFinite(expires) || expires <= Date.now()) throw new Error("Expired evaluation deadline.");
  const signal = AbortSignal.timeout(Math.max(1, expires - Date.now())), network = globalThis.fetch;
  let model: Awaited<ReturnType<typeof resolveEvaluationModel>> | undefined;
  let reserve: RequestReserve | undefined;
  let transport: ReturnType<typeof liveTransport> | undefined;
  if (config.live) permitLive(config, plan, plan.planSHA256, process.env);
  if (config.live && policy==="v2-investigation") {
    reserve = new RequestReserve(6, plan.perRequestUSD!, plan.perRequestUSD! * 6, expires);
    transport = liveTransport(config, endpoint.href, reserve, network, signal, plan.maximumInputTokens!/plan.maximumModelRequests); globalThis.fetch = transport.fetch;
    model = await resolveEvaluationModel(config.model!, config.maxOutputTokens, transport.permit);
  } else globalThis.fetch = (input, init) => {
    if (new URL(input instanceof Request ? input.url : String(input)).href !== endpoint.href) throw new Error("Offline mode forbids provider network access.");
    return network(input, { ...init, redirect: "error", signal: AbortSignal.any([signal, ...(init?.signal ? [init.signal] : [])]) });
  };
  try {
    const api = createFleetClient(endpoint.href, process.env.FLEETOPS_AGENT07_PROPOSER ?? "");
    const result = await trial(scenario, policy, api, { go: true, live: config.live, model, requestedModel: config.model ? `openai/${config.model}` : undefined, allowSubmit: config.testHarnessApprovals===true, signal });
    if (reserve) result.cost = { estimatedMaximumUSD: reserve.reservedUSD, actualBilledUSD: null, method: "validated review; full-context worst-case reservation retained even for uncertain requests; NOT actual billing", providerHTTPRequests: reserve.calls };
    if (transport) result.modelCalls = { localAdapterFailures:null, ...transport.measurements };
    if (config.live && policy==="v2-investigation" && result.status==="accepted" && (!reserve || reserve.calls!==result.completion?.modelRequests)) {
      result.status="generation-failed"; result.failure="unverified_live_transport"; result.groundingAccepted=false; result.expectationMet=false;
    }
    process.stdout.write(JSON.stringify(result));
  } finally { globalThis.fetch = network; }
}
export async function agent07CLI(args: string[]): Promise<void> {
  const config = parseArgs(args), plan = reviewed(config);
  if (config.plan) { process.stdout.write(JSON.stringify({ version: dataset.version, mode: config.live ? "live-plan-only" : "offline-plan-only", ...plan }, null, 2)+"\n"); return; }
  if (config.live) permitLive(config, plan, plan.planSHA256, process.env);
  if (config.go && !config.live && !config.testHarnessApprovals) throw new Error("Offline Go paired experiments require explicit --test-harness-approvals; the default offline contract evaluation has no execution authority.");
  process.stderr.write(`AGENT-07 ${config.live ? "REVIEWED LIVE" : "OFFLINE"} plan before generation: ${JSON.stringify(plan)}\n`);
  let report: object;
  if (config.go) {
    isolatedDatabase(process.env.FLEETOPS_TEST_DATABASE_URL);
    const dir = resolve(root, "evals/dist/agent07"); mkdirSync(dir, { recursive: true }); const path = resolve(dir, `run-${process.pid}-${Date.now()}.json`);
    const result = spawnSync(process.env.FLEETOPS_EVAL_GO ?? "go", ["test", "-tags", "fleetops_test", "./fleet", "-run", "^TestAgent07PairedEvaluation$", "-count=1", "-timeout", "11m"], {
      cwd: resolve(root, "backend"), encoding: "utf8", timeout: config.maxElapsedMs + 60000, maxBuffer: 16 * 1024 * 1024,
      env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off", FLEETOPS_REQUIRE_DB: "1", FLEETOPS_AGENT07_CHECK: "1", FLEETOPS_AGENT07_CONFIG: JSON.stringify(config), FLEETOPS_AGENT07_REPORT: path } });
    if (result.status !== 0) { process.stderr.write(result.stdout); throw new Error("Isolated Go evaluation did not complete; no automatic retry. Inspect local test diagnostics."); }
    const measured = JSON.parse(readFileSync(path, "utf8")) as { rows: Branch[]; stoppedReason: string };
    if(measured.stoppedReason) process.exitCode=1;
    report = { ...measured, plan, aggregate: aggregate(measured.rows.flatMap(r => r.decision ? [r.decision] : [])), outcomes: outcomeSummary(measured.rows), actualBilledCostUSD: null,
      limitation: "Exploratory small deterministic dataset. Test-harness approvals are not actual human approvals. Intervention benefit is separate from AI decision quality; archived Phase-5 and synthetic 17-case results are not pooled." };
  } else report = { ...await offlineEvaluation(dataset.scenarios.filter(s => config.scenarios.includes(s.id)), config.trials, config.maxElapsedMs), plan };
  const output = JSON.stringify(report, null, 2)+"\n";
  if (config.output) writeFileSync(config.output, output, { mode: 0o600 });
  process.stdout.write(output);
}
