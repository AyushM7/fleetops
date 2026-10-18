import { type AgentRecord } from "./agentRecords";
import type { AgentFeed } from "./agentRuns";

export function agentFixture(): AgentRecord {
  const r3 = { jobId: "J1", fromRobotId: "R1", toRobotId: "R3" }, r4 = { ...r3, toRobotId: "R4" }, r5 = { ...r3, toRobotId: "R5" };
  const proposalId = `P${"d".repeat(32)}`;
  return { id: `A${"b".repeat(32)}`, runtimeId: `P${"a".repeat(32)}`, observationId: "c".repeat(64), startedAt: "2026-10-18T10:57:01.000Z", completedAt: "2026-10-18T10:57:01.072Z",
    serverSubmissionOutcome: "pending_proposal_created", serverElapsedMs: 72, serverVerifiedDecision: true, estimatedCostStatus: "unavailable", proposalId,
    agentReportedMetadata: { version: "agent-run-v1", contract: "recovery-decision-v2", objective: "compare_tradeoffs", reportedSource: "mocked", requestedModel: "offline-agent05/scripted" },
    serverVerifiedObservation: { tick: 42, poisoned: false, blocked: true, incidents: [{ robotId: "R1", type: "traffic_blocked", severity: "high" }, { robotId: "R2", type: "robot_unresponsive", severity: "high" }],
      candidates: [[r3, 1, 13], [r4, 6, 18], [r5, 4, 16]].map(([key, pickup, required]) => ({ key: key as typeof r3, metrics: { pickupSteps: pickup as number,
        deliverySteps: 2, chargingExitSteps: 5, totalRouteSteps: (pickup as number) + 7, battery: 98, requiredBattery: required as number, missionHeadroom: 98 - (required as number), admissionHeadroom: 68 } })) },
    agentReportedCompletion: { status: "accepted", failure: "none", latencyMs: 51, modelRequests: 4, toolExecutions: 6, reportedActualModel: "", submissionCheck: "current",
      steps: [{ kind: "request", tool: "" }, { kind: "tool", tool: "readIncident" }, { kind: "tool", tool: "listRecoveryCandidates" }, { kind: "tool", tool: "readIncident" }, { kind: "tool", tool: "listRecoveryCandidates" },
        { kind: "request", tool: "" }, { kind: "tool", tool: "readRobots" }, { kind: "tool", tool: "readJobs" }, { kind: "request", tool: "" }, { kind: "request", tool: "" }],
      reportedUsage: { status: "reported", inputTokens: 4, outputTokens: 4, totalTokens: 8 },
      decision: { kind: "select", selected: r5, basis: "admitted_option", reason: "", comparisons: [
        { left: r5, right: r3, metric: "pickupSteps", leftValue: 4, rightValue: 1, relation: "greater" },
        { left: r5, right: r4, metric: "pickupSteps", leftValue: 4, rightValue: 6, relation: "less" }] } },
    proposal: { id: proposalId, action: "reassign_unstarted_job", jobId: "J1", fromRobotId: "R1", toRobotId: "R5", robotId: "", stationId: "", currentRuntime: true,
      state: "pending", execution: "none", submittedBy: "scripted-proposer", createdAt: "2026-10-18T10:57:01.072Z", expiresAt: "2030-10-18T10:58:01Z" } };
}
export function outerFixture(run = agentFixture()): { id: string; runtimeId: string; observationId: string; data: string; proposal: AgentRecord["proposal"] } {
  const { proposal, ...data } = run; return { id: run.id, runtimeId: run.runtimeId, observationId: run.observationId, data: JSON.stringify(data), proposal };
}
export function activityFixture(run = agentFixture()): AgentFeed {
  return { phase: "ready", runs: [run], runtimeId: run.runtimeId, current: { observationId: "e".repeat(64), tick: 43, poisoned: false, jobs: [{ id: "J1", assignedRobot: "R5", status: "assigned" }] }, updatedAt: new Date("2026-10-18T10:57:02Z") };
}
export function responseFixture(runs = [outerFixture()]): object { const feed = activityFixture(); return { data: { agentRuns: runs, fleetState: feed.current } }; }
