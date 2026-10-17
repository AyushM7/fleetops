import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { validateReasoning, validateSnapshot } from "../../agent/src/reasoning.js";
import type { FleetState } from "../../agent/src/contracts.js";
import { observerAdapter } from "./dataset-adapter.js";
import { scoreCase, type Action, type OfflineAdapter, type ScoringResult } from "./index.js";

interface Observation {
  id: string; source: string; goal: string; targetJob: string; expectedAction: Action | null;
  seed: number; horizonTicks: number; initialHash: string; input: FleetState;
}
interface SimState { tick: number; seed: number; robots: { id: string; battery: number }[]; jobs: { id: string; status: string }[] }
interface Outcome {
  final: SimState; target: { id: string; status: string; completionTick: number | null; timeToCompletion: number | null; censored: boolean; completedBeforeWindow: boolean } | null;
  jobsCompletedInWindow: number; blockedRobotTicks: number; chargingRobotTicks: number; occupiedStationTicks: number; totalFinalBattery: number;
}
interface Experiment {
  scenario: { id: string; goal: string }; initialHash: string; seed: number; horizonTicks: number; startTick: number; endTick: number;
  baselineInitial: SimState; treatmentInitial: SimState; pairedInitialEqual: boolean;
  intervention: { recommendation: Action | null; submission: string; approval: string; operator: string; execution: string; safetyValidation: string; executionAttempted: boolean };
  baseline: Outcome; treatment: Outcome; criterion: string; criterionMet: boolean;
  effects: { finalStoredBatteryDelta: number; blockedRobotTicksDelta: number; occupiedStationTicksDelta: number };
  successfulIntervention: boolean; targetDeliveryImproved: boolean;
}
export interface RecoveryReport {
  version: "p5-03-v1"; mode: "deterministic-direct-command-domain-lifecycle";
  actualLLM: "not_measured"; actualHumanApproval: "not_measured"; durableAudit: "not_measured";
  recommendations: ScoringResult[]; experiments: Experiment[];
  totals: { experiments: number; criteriaMet: number; failed: number; executedInterventions: number; failedInterventions: number; noIntervention: number; targetDeliveryImprovements: number };
}
export type RecoveryTransport = (mode: "observe" | "run", requests?: unknown) => unknown;
const backend = fileURLToPath(new URL("../../backend", import.meta.url));

function offlineGo(): string {
  if (process.env.FLEETOPS_EVAL_GO) return process.env.FLEETOPS_EVAL_GO;
  const env = { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" };
  const cache = spawnSync("go", ["env", "GOMODCACHE"], { encoding: "utf8", env });
  if (cache.status !== 0) throw new Error("Go is required for recovery experiments.");
  const version = /^go ([\d.]+)$/m.exec(readFileSync(join(backend, "go.mod"), "utf8"))?.[1];
  const arch = process.arch === "x64" ? "amd64" : process.arch;
  const platform = process.platform === "win32" ? "windows" : process.platform;
  const executable = process.platform === "win32" ? "go.exe" : "go";
  const cached = join(cache.stdout.trim(), "golang.org", `toolchain@v0.0.1-go${version}.${platform}-${arch}`, "bin", executable);
  return existsSync(cached) ? cached : "go";
}
export const goRecoveryTransport: RecoveryTransport = (mode, requests) => {
  const args = ["run", "-tags", "fleetops_test", "./evalrecovery", mode];
  // The test operator decision is a trusted harness option, never model output.
  if (mode === "run") args.push("--test-operator", "approve");
  const result = spawnSync(offlineGo(), args, { cwd: resolve(backend), encoding: "utf8", maxBuffer: 16 * 1024 * 1024,
    timeout: 120000, input: requests === undefined ? undefined : JSON.stringify(requests),
    env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" } });
  if (result.status !== 0) throw new Error(`Go recovery ${mode} failed: ${result.stderr || result.error?.message || "unknown failure"}`);
  return JSON.parse(result.stdout);
};

export async function runRecovery(transport: RecoveryTransport = goRecoveryTransport, adapter: OfflineAdapter = observerAdapter): Promise<RecoveryReport> {
  const observations = transport("observe") as Observation[];
  if (!Array.isArray(observations) || observations.length !== 5 || new Set(observations.map(o => o.id)).size !== 5) throw new Error("Missing or ambiguous Go scenarios.");
  const recommendations: ScoringResult[] = [];
  const requests = [];
  for (const o of observations) {
    const input = validateSnapshot(o.input);
    const actual = await adapter(structuredClone(input), o.id);
    const score = scoreCase({ id: o.id, provenance: o.source, input, expected: { action: o.expectedAction,
      incidents: input.activeIncidents.map(i => ({ scope: "snapshot", tick: input.tick, robotId: i.robotId, type: i.type })) } }, actual);
    if (score.status !== "passed") throw new Error(`Recommendation rejected for ${o.id}: ${score.status}`);
    const reasoning = validateReasoning(actual.reasoning, input);
    const a = reasoning.proposedAction;
    const identity = a === null ? null : a.action === "SetCharging" ? { action: a.action, purpose: a.purpose, robotId: a.robotId, stationId: a.stationId } :
      { action: a.action, purpose: a.purpose, jobId: a.jobId, fromRobotId: a.fromRobotId, toRobotId: a.toRobotId };
    recommendations.push(score); requests.push({ id: o.id, initialHash: o.initialHash, action: identity });
  }
  const experiments = transport("run", requests) as Experiment[];
  if (!Array.isArray(experiments) || experiments.length !== observations.length) throw new Error("Incomplete Go experiment results.");
  for (const [i, e] of experiments.entries()) {
    if (e.scenario.id !== observations[i].id || e.initialHash !== observations[i].initialHash || !e.pairedInitialEqual ||
      e.seed !== observations[i].seed || e.horizonTicks !== observations[i].horizonTicks || typeof e.criterionMet !== "boolean" ||
      e.startTick !== observations[i].input.tick || e.endTick !== e.startTick + e.horizonTicks ||
      e.baseline.final.tick !== e.endTick || e.treatment.final.tick !== e.endTick) throw new Error("Invalid or mismatched paired experiment.");
  }
  return { version: "p5-03-v1", mode: "deterministic-direct-command-domain-lifecycle", actualLLM: "not_measured", actualHumanApproval: "not_measured", durableAudit: "not_measured",
    recommendations, experiments, totals: { experiments: experiments.length, criteriaMet: experiments.filter(e => e.criterionMet).length,
      failed: experiments.filter(e => !e.criterionMet).length, executedInterventions: experiments.filter(e => e.successfulIntervention).length,
      failedInterventions: experiments.filter(e => e.intervention.recommendation !== null && !e.successfulIntervention).length,
      noIntervention: experiments.filter(e => e.intervention.recommendation === null).length,
      targetDeliveryImprovements: experiments.filter(e => e.targetDeliveryImproved).length } };
}
export function summarizeRecovery(report: RecoveryReport): string {
  const target = (o: Outcome): string => !o.target ? "no target job" : o.target.completedBeforeWindow ? "done before window; delivery tick unmeasured" : o.target.censored ? `${o.target.status}; censored` :
    `${o.target.status} at tick ${o.target.completionTick} (+${o.target.timeToCompletion})`;
  return ["Deterministic Go recovery; isolated domain lifecycle + explicit test operator; NOT a persisted end-to-end or live-LLM experiment.",
    ...report.experiments.map(e => `${e.scenario.id}: seed=${e.seed}, ticks=${e.startTick}..${e.endTick}; baseline ${target(e.baseline)}; treatment ${target(e.treatment)}; execution=${e.intervention.execution}; criterion=${e.criterionMet ? "PASS" : "FAIL"}` +
      (e.scenario.goal === "battery" ? `; R1 final battery ${e.baseline.final.robots[0].battery}% vs ${e.treatment.final.robots[0].battery}%; occupied-station samples ${e.baseline.occupiedStationTicks} vs ${e.treatment.occupiedStationTicks}` :
        e.targetDeliveryImproved ? `; stored-battery delta=${e.effects.finalStoredBatteryDelta}; blocked-robot samples delta=${e.effects.blockedRobotTicksDelta}` : "")),
    `${report.totals.criteriaMet}/${report.totals.experiments} experiment criteria met; ${report.totals.executedInterventions} commands succeeded; ${report.totals.targetDeliveryImprovements} target-delivery improvement(s).`,
    "Job-free charging measures stored battery and charging/station occupancy, not job throughput. Actual human approval, durable audits and LLM quality: unmeasured."].join("\n");
}
