export interface Proposal {
  id: string; action: "set_charging" | "reassign_unstarted_job"; robotId: string; stationId: string;
  jobId: string; fromRobotId: string; toRobotId: string;
  state: "pending" | "approved" | "rejected" | "expired";
  execution: "none" | "succeeded" | "failed" | "unknown";
  submittedBy: string; currentRuntime: boolean; createdAt: string; expiresAt: string;
}
export type Decision = "approve" | "reject";
export interface DecisionResult {
  proposalId: string; state: string; execution: string;
  auditCommitted: boolean; executionAttempted: boolean; error: string | null;
}
export interface InboxFeed {
  phase: "loading" | "ready" | "forbidden" | "unavailable";
  proposals: Proposal[]; updatedAt: Date | null;
}
export interface InboxPolling { refresh: () => void; stop: () => void; settled: (result: DecisionResult) => void }
export class ProposalError extends Error {
  constructor(public readonly kind: "authentication" | "forbidden" | "unavailable") { super(kind); }
}
const fields = "id action robotId stationId jobId fromRobotId toRobotId state execution submittedBy currentRuntime createdAt expiresAt";
export const pendingQuery = `{pendingProposals{${fields}}}`;
const record = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null;
const unavailable = (): never => { throw new ProposalError("unavailable"); };

async function request(token: string, query: string, variables: Record<string, unknown>, signal: AbortSignal): Promise<Record<string, unknown>> {
  const controller = new AbortController();
  const cancel = () => controller.abort();
  signal.addEventListener("abort", cancel, { once: true });
  if (signal.aborted) controller.abort();
  const timeout = setTimeout(cancel, 8000);
  try {
    const response = await fetch("/graphql", { method: "POST", credentials: "omit", cache: "no-store", redirect: "error", signal: controller.signal,
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ query, variables }) });
    if (response.status === 401 || response.status === 403) throw new ProposalError("authentication");
    if (!response.ok) return unavailable();
    const body: unknown = await response.json();
    if (!record(body)) return unavailable();
    if (body.errors) {
      if (Array.isArray(body.errors) && body.errors.some(error => record(error) && record(error.extensions) && error.extensions.code === "FORBIDDEN")) throw new ProposalError("forbidden");
      return unavailable();
    }
    if (!record(body.data)) return unavailable();
    return body.data;
  } finally { clearTimeout(timeout); signal.removeEventListener("abort", cancel); }
}

export function parseProposal(value: unknown): Proposal {
  if (!record(value) || !["set_charging", "reassign_unstarted_job"].includes(value.action as string) || typeof value.currentRuntime !== "boolean") return unavailable();
  for (const field of ["id", "robotId", "stationId", "jobId", "fromRobotId", "toRobotId", "submittedBy", "createdAt", "expiresAt"]) if (typeof value[field] !== "string") return unavailable();
  if (value.action === "set_charging" ? !value.robotId || !value.stationId || value.jobId || value.fromRobotId || value.toRobotId
    : !value.jobId || !value.fromRobotId || !value.toRobotId || value.fromRobotId === value.toRobotId || value.robotId || value.stationId) return unavailable();
  if (!value.id || !Number.isFinite(Date.parse(value.createdAt as string)) || !Number.isFinite(Date.parse(value.expiresAt as string))) return unavailable();
  if (!["pending", "approved", "rejected", "expired"].includes(value.state as string) || !["none", "succeeded", "failed", "unknown"].includes(value.execution as string)) return unavailable();
  return value as unknown as Proposal;
}
export async function fetchPending(token: string, signal: AbortSignal): Promise<Proposal[]> {
  const data = await request(token, pendingQuery, {}, signal);
  if (!Array.isArray(data.pendingProposals) || data.pendingProposals.length > 50) return unavailable();
  return data.pendingProposals.map(value => {
    const proposal = parseProposal(value);
    if (!proposal.currentRuntime || proposal.state !== "pending") return unavailable();
    return proposal;
  });
}
export async function fetchProposal(token: string, id: string, signal: AbortSignal): Promise<Proposal | null> {
  const data = await request(token, `query($id:ID!){proposal(id:$id){${fields}}}`, { id }, signal);
  if (data.proposal === null) return null;
  const proposal = parseProposal(data.proposal);
  if (proposal.id !== id) return unavailable();
  return proposal;
}
export async function decideProposal(token: string, id: string, decision: Decision, signal: AbortSignal): Promise<DecisionResult> {
  const operation = decision === "approve" ? "approveProposal" : "rejectProposal";
  const data = await request(token, `mutation($id:ID!){${operation}(id:$id){proposalId state execution auditCommitted executionAttempted error}}`, { id }, signal);
  const result = data[operation];
  if (!record(result) || result.proposalId !== id || typeof result.state !== "string" || typeof result.execution !== "string" || typeof result.auditCommitted !== "boolean" || typeof result.executionAttempted !== "boolean" || (result.error !== null && typeof result.error !== "string")) return unavailable();
  if (!["", "pending", "approved", "rejected", "expired"].includes(result.state) || !["none", "succeeded", "failed", "unknown"].includes(result.execution)) return unavailable();
  if (result.execution === "succeeded" && !["approved", "expired"].includes(result.state)) return unavailable();
  return result as unknown as DecisionResult;
}

// A manual refresh supersedes older reads. Mutations are never retried here.
export function pollPending(token: string, emit: (feed: InboxFeed) => void, unauthorized: () => void): InboxPolling {
  let stopped = false;
  let generation = 0;
  let controller: AbortController | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let proposals: Proposal[] = [];
  let updatedAt: Date | null = null;
  let phase: InboxFeed["phase"] = "loading";
  const publish = (next: InboxFeed["phase"]) => { phase = next; emit({ phase, proposals, updatedAt }); };
  const refresh = async () => {
    controller?.abort(); clearTimeout(timer);
    const version = ++generation;
    const current = new AbortController(); controller = current;
    let retry = true;
    try {
      const next = await fetchPending(token, current.signal);
      if (stopped || version !== generation) return;
      proposals = next; updatedAt = new Date(); publish("ready");
    } catch (error) {
      if (stopped || version !== generation) return;
      if (error instanceof ProposalError && error.kind === "authentication") { stopped = true; unauthorized(); return; }
      if (error instanceof ProposalError && error.kind === "forbidden") { retry = false; proposals = []; updatedAt = null; publish("forbidden"); }
      else publish("unavailable");
    } finally {
      if (!stopped && version === generation && retry) timer = setTimeout(() => { void refresh(); }, 2000);
    }
  };
  publish("loading"); void refresh();
  return {
    refresh: () => { if (!stopped) void refresh(); },
    stop: () => { stopped = true; generation++; clearTimeout(timer); controller?.abort(); },
    settled: result => {
      // A committed server response is authoritative even while the next read is delayed.
      if (!stopped && result.auditCommitted && ["approved", "rejected", "expired"].includes(result.state)) {
        proposals = proposals.filter(proposal => proposal.id !== result.proposalId); publish(phase);
      }
    },
  };
}
