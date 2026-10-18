import { z } from "zod";
import type { FleetAPI } from "./graphql.js";
import type { FleetState } from "./contracts.js";
import type { RecoveryDecisionResult } from "./decision-agent.js";
import type { CandidateKey, RecoveryDecision } from "./decision-contracts.js";
import type { InvestigationTrace } from "./investigation-budget.js";
import { submitReviewedAction } from "./submission.js";
import { mutationSchema, observationIdSchema } from "./contracts.js";

const identifier = z.string().regex(/^[A-Za-z0-9_-]{1,128}$/);
export const agentRunHandleSchema = z.object({ id: identifier, runtimeId: identifier, observationId: observationIdSchema });
export type AgentRunHandle = z.infer<typeof agentRunHandleSchema>;
export interface AgentRunMetadata { version: "agent-run-v1"; contract: "recovery-decision-v2"; objective: string;
  reportedSource: "live-model" | "mocked" | "deterministic" | "recorded"; requestedModel: string }
export interface AgentRunCompletion {
  steps: { kind: "request" | "tool"; tool: string }[];
  status: "accepted" | "rejected" | "failed"; failure: string; latencyMs: number; modelRequests: number; toolExecutions: number;
  reportedUsage: { status: "reported" | "partial" | "unavailable"; inputTokens: number | null; outputTokens: number | null; totalTokens: number | null };
  reportedActualModel: string; submissionCheck: string;
  decision: { kind: "select" | "abstain"; selected: CandidateKey | null;
    basis: string; reason: string; comparisons: RecoveryDecision["comparisons"] } | null;
}
export interface AgentRunFinish { runtimeId: string; observationId: string; completion: AgentRunCompletion; submit: { expiresAt: string } | null }
export const agentReceiptSchema = z.object({ runId: identifier, recorded: z.literal(true), denial: z.string().max(64), proposalResult: mutationSchema.nullable() });
export type AgentRunReceipt = z.infer<typeof agentReceiptSchema>;
export function completionRecord(status: AgentRunCompletion["status"], decision: RecoveryDecision | null, trace: InvestigationTrace, latency: number): AgentRunCompletion {
  const usage = !trace.usageReports ? "unavailable" : trace.usageReports === trace.requests ? "reported" : "partial";
  const selected = decision?.decision.kind === "select" ? decision.decision.selected : null;
  return { steps: trace.events.map(e => ({ kind: e.kind, tool: e.kind === "tool" ? e.name : "" })),
    status, failure: status === "accepted" ? "none" : status === "rejected" ? "grounding_rejected" : trace.failureCategory ?? "investigation_failed",
    latencyMs: Math.max(0, Math.round(latency)), modelRequests: trace.requests, toolExecutions: trace.toolCalls,
    reportedUsage: { status: usage, inputTokens: usage === "unavailable" ? null : trace.inputTokens,
      outputTokens: usage === "unavailable" ? null : trace.outputTokens, totalTokens: usage === "unavailable" ? null : trace.totalTokens },
    reportedActualModel: trace.reportedActualModel ?? "", submissionCheck: "not_requested",
    decision: decision ? { kind: decision.decision.kind, selected,
      basis: decision.decision.kind === "select" ? decision.decision.basis : "", reason: decision.decision.kind === "abstain" ? decision.decision.reason : "",
      comparisons: decision.comparisons } : null };
}
export async function finishDecisionRun(api: FleetAPI, handle: AgentRunHandle, snapshot: FleetState, completion: AgentRunCompletion,
  action: RecoveryDecisionResult["recommendedAction"], allowSubmit: boolean) {
  let attempted = false;
  let persistence: "recorded" | "unknown" = "unknown";
  async function persist(expiresAt?: string) {
    if (attempted) throw new Error("Agent completion already attempted.");
    attempted = true;
    const receipt = await api.finishAgentRun!(handle.id, { runtimeId: handle.runtimeId, observationId: handle.observationId, completion,
      submit: expiresAt ? { expiresAt } : null });
    if (receipt.runId !== handle.id || !receipt.recorded) throw new Error("Invalid agent completion receipt.");
    persistence = "recorded"; return receipt;
  }
  const submission = await submitReviewedAction({ ...api, submitReassignment: async input => {
    completion.submissionCheck = "current";
    const receipt = await persist(input.expiresAt);
    if (!receipt.proposalResult) throw new Error("Proposal receipt unavailable.");
    return receipt.proposalResult;
  } }, snapshot, action, { allowSubmit });
  if (!attempted) {
    completion.submissionCheck = submission.submissionCheck;
    try { await persist(); } catch { /* A lost acknowledgement can follow COMMIT. No automatic retry. */ }
  }
  return { ...submission, provenance: { runId: handle.id, status: persistence as "recorded" | "unknown" } };
}
