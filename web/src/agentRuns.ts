import { authentication, isLocalDemo } from "./demo";
import { AgentDataError, id, invalid, number, object, observation, parseAgentRecord, shape, type AgentRecord } from "./agentRecords";

export interface ObservedJob { id: string; status: "pending" | "assigned" | "in_progress" | "done"; assignedRobot: string }
export interface RunSnapshot { observationId: string; tick: number; poisoned: boolean; jobs: ObservedJob[] }
export interface AgentFeed {
  phase: "loading" | "ready" | "forbidden" | "unavailable" | "malformed" | "demo" | "paused" | "denied";
  runs: AgentRecord[]; runtimeId: string | null; current: RunSnapshot | null; updatedAt: Date | null;
}
export class AgentReadError extends Error {
  constructor(public readonly kind: "authentication" | "forbidden" | "unavailable" | "denied") { super("Agent provenance read failed."); }
}
const proposalFields = "id action robotId stationId jobId fromRobotId toRobotId state execution submittedBy currentRuntime createdAt expiresAt";
export const agentRunsQuery = `query AgentActivity($limit:Int!){
  fleetState { observationId tick poisoned jobs { id status assignedRobot } }
  agentRuns(limit:$limit){id runtimeId observationId data proposal{${proposalFields}}}
}`;
function parseCurrent(v: unknown): RunSnapshot {
  const r = shape(v, "observationId tick poisoned jobs");
  if (typeof r.poisoned !== "boolean" || !Array.isArray(r.jobs) || r.jobs.length > 1000) return invalid();
  const jobs = r.jobs.map(v => { const j = shape(v, "id status assignedRobot");
    if (!["pending", "assigned", "in_progress", "done"].includes(j.status as string)) return invalid();
    return { id: id(j.id), assignedRobot: j.assignedRobot === "" ? "" : id(j.assignedRobot), status: j.status as ObservedJob["status"] }; });
  if (new Set(jobs.map(j => j.id)).size !== jobs.length) return invalid();
  return { observationId: observation(r.observationId), tick: number(r.tick), poisoned: r.poisoned, jobs };
}
async function readJSON(response: Response, controller: AbortController): Promise<unknown> {
  const reader = response.body?.getReader(); if (!reader) return invalid();
  const chunks: Uint8Array[] = []; let size = 0;
  try { for (;;) { const next = await reader.read(); if (next.done) break;
    size += next.value.byteLength; if (size > 2_097_152) { controller.abort(); return invalid(); } chunks.push(next.value); }
  } finally { reader.releaseLock(); }
  const bytes = new Uint8Array(size); let offset = 0; for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  try { return JSON.parse(new TextDecoder().decode(bytes)); } catch { return invalid(); }
}
export async function fetchAgentRuns(token: string, signal: AbortSignal): Promise<{ runs: AgentRecord[]; runtimeId: string | null; current: RunSnapshot }> {
  const controller = new AbortController(), abort = () => controller.abort();
  signal.addEventListener("abort", abort, { once: true }); if (signal.aborted) abort();
  const timeout = setTimeout(abort, 8000);
  try {
    const response = await fetch("/graphql", { method: "POST", ...authentication(token), cache: "no-store", redirect: "error", signal: controller.signal,
      body: JSON.stringify({ query: agentRunsQuery, variables: { limit: 10 } }) });
    if (response.status === 401) throw new AgentReadError("authentication");
    if (response.status === 403) throw new AgentReadError("forbidden");
    if (!response.ok) throw new AgentReadError("unavailable");
    const body = await readJSON(response, controller);
    if (!object(body)) return invalid();
    if (body.errors) {
      const errors = Array.isArray(body.errors) ? body.errors : [];
      const code = (value: unknown, expected: string) => object(value) && object(value.extensions) && value.extensions.code === expected;
      if (errors.some(e => code(e, "FORBIDDEN"))) throw new AgentReadError("forbidden");
      if (errors.some(e => code(e, "AGENT_PROVENANCE_DENIED"))) throw new AgentReadError("denied");
      throw new AgentReadError("unavailable");
    }
    if (!object(body.data) || !Array.isArray(body.data.agentRuns) || body.data.agentRuns.length > 10) return invalid();
    const current = parseCurrent(body.data.fleetState), runs = body.data.agentRuns.map(parseAgentRecord);
    if (new Set(runs.map(r => r.id)).size !== runs.length || new Set(runs.map(r => r.runtimeId)).size > 1 ||
      new Set(runs.flatMap(r => r.proposalId ? [r.proposalId] : [])).size !== runs.filter(r => r.proposalId).length ||
      runs.some(r => r.serverVerifiedObservation.tick > current.tick)) return invalid();
    return { runs, runtimeId: runs[0]?.runtimeId ?? null, current };
  } finally { clearTimeout(timeout); signal.removeEventListener("abort", abort); }
}
export interface AgentPolling { refresh: () => void; stop: () => void }
export function pollAgentRuns(token: string, emit: (feed: AgentFeed) => void, unauthorized: () => void): AgentPolling {
  let stopped = false, epoch = 0;
  let timer: ReturnType<typeof setTimeout> | undefined, controller: AbortController | undefined;
  const refresh = async () => {
    clearTimeout(timer); controller?.abort(); const version = ++epoch; controller = new AbortController();
    let retry = true;
    try {
      const next = await fetchAgentRuns(token, controller.signal);
      if (!stopped && version === epoch) emit({ phase: "ready", ...next, updatedAt: new Date() });
    } catch (error) {
      if (stopped || version !== epoch) return;
      if (error instanceof AgentReadError && error.kind === "authentication") { stopped = true; unauthorized(); return; }
      const phase = error instanceof AgentReadError ? error.kind : error instanceof AgentDataError ? "malformed" : "unavailable";
      // Never keep selectable prior-runtime records after a denied or failed refresh.
      emit({ phase: phase === "authentication" ? "unavailable" : phase, runs: [], runtimeId: null, current: null, updatedAt: null });
      retry = !["forbidden", "denied", "malformed"].includes(phase);
    } finally { if (!stopped && version === epoch && retry) timer = setTimeout(() => { void refresh(); }, 2000); }
  };
  const publish = (phase: AgentFeed["phase"]) => emit({ phase, runs: [], runtimeId: null, current: null, updatedAt: null });
  // The deterministic gateway does not grant provenance access. Do not substitute its evidence archive.
  if (isLocalDemo()) { publish("demo"); return { refresh() {}, stop() {} }; }
  publish("loading"); void refresh();
  return { refresh: () => { if (!stopped) void refresh(); }, stop: () => { stopped = true; epoch++; clearTimeout(timer); controller?.abort(); } };
}
