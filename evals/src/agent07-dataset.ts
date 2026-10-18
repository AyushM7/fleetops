import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import type { FleetState } from "../../agent/src/contracts.js";
import type { CandidateKey, DecisionObjective } from "../../agent/src/decision-contracts.js";
import { fleetFixture, recoveryDecisionState } from "../../agent/src/fixtures.js";
import { validateSnapshot } from "../../agent/src/reasoning.js";

export type Policy = "first-eligible" | "minimum-pickup" | "maximum-headroom" | "v2-investigation";
export const policies: Policy[] = ["first-eligible", "minimum-pickup", "maximum-headroom", "v2-investigation"];
export interface Scenario {
  id: string; variant: string; objective: DecisionObjective; expected: string;
  quality: "pareto-only" | "pickup-regret" | "headroom-regret" | "not-applicable";
  comparable: boolean; probe?: "false-evidence" | "invalid-tool";
}
const raw = readFileSync(new URL("../fixtures/agent07-v1.json", import.meta.url), "utf8").replaceAll("\r\n", "\n");
export const dataset = JSON.parse(raw) as { version: "agent07-v1"; fixtureVersion: string; seed: number; horizonTicks: number; measurementFaults: []; scenarios: Scenario[] };
export const datasetSHA256 = createHash("sha256").update(raw).digest("hex");
export const key = (c: CandidateKey): CandidateKey => ({ jobId: c.jobId, fromRobotId: c.fromRobotId, toRobotId: c.toRobotId });
export const sameKey = (a: CandidateKey, b: CandidateKey): boolean => JSON.stringify(key(a)) === JSON.stringify(key(b));
export function candidates(state: FleetState) {
  if (state.poisoned) return [];
  return state.reassignmentCandidates.filter(c => state.activeIncidents.some(i => i.type === "traffic_blocked" && i.robotId === c.fromRobotId))
    .map(c => ({ ...c, headroom: state.robots.find(r => r.id === c.toRobotId)!.battery - Math.max(30, c.requiredBattery) }));
}
export function baseline(state: FleetState, policy: Exclude<Policy, "v2-investigation">): CandidateKey | null {
  const rows = candidates(state);
  if (policy === "minimum-pickup") rows.sort((a, b) => a.pickupSteps - b.pickupSteps);
  if (policy === "maximum-headroom") rows.sort((a, b) => b.headroom - a.headroom);
  return rows[0] ? key(rows[0]) : null;
}
// These deliberately synthetic fixtures exercise the production TS tools, never Go admission.
export function syntheticState(s: Scenario): FleetState {
  let state = s.variant === "healthy" ? fleetFixture() : recoveryDecisionState(s.variant === "tradeoff" ? "tradeoff" : s.variant === "tie" ? "tie" : s.variant === "none" ? "none" : "non_first_minimum");
  state = structuredClone(state);
  if (s.variant === "blocked") for (const [i, c] of state.reassignmentCandidates.entries()) { c.pickupSteps = [1, 6, 4][i]; c.requiredBattery = c.pickupSteps + 12; }
  if (s.variant === "poisoned") { state.poisoned = true; state.reassignmentCandidates = []; }
  state.observationId = createHash("sha256").update(`SYNTHETIC-NOT-GO:${dataset.fixtureVersion}:${s.id}`).digest("hex");
  return validateSnapshot(state);
}
export function quality(s: Scenario, state: FleetState, selected: CandidateKey | null) {
  const rows = candidates(state), chosen = selected && rows.find(c => sameKey(c, selected));
  if (!chosen) return { admitted: false, regret: null, dominated: null, objectiveMet: selected === null && rows.length === 0 };
  const pickupRegret = chosen.pickupSteps - Math.min(...rows.map(c => c.pickupSteps));
  const headroomRegret = Math.max(...rows.map(c => c.headroom)) - chosen.headroom;
  return { admitted: true, regret: s.quality === "pickup-regret" ? pickupRegret : s.quality === "headroom-regret" ? headroomRegret : null,
    dominated: rows.some(c => c.pickupSteps <= chosen.pickupSteps && c.headroom >= chosen.headroom && (c.pickupSteps < chosen.pickupSteps || c.headroom > chosen.headroom)),
    objectiveMet: s.expected === "minimum-pickup" ? pickupRegret === 0 : s.expected === "maximum-headroom" ? headroomRegret === 0 : ["admitted","admitted-or-tradeoff"].includes(s.expected) };
}
