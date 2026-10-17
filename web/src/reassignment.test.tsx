import { renderToString } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "./App";
import { InboxView, type Receipt } from "./ApprovalInbox";
import { parseSnapshot, type FleetSnapshot } from "./fleet";
import { decideProposal, fetchPending, fetchProposal, parseProposal, type Proposal } from "./proposals";

function snapshot(): FleetSnapshot {
  const source = { id: "R1", x: 0, y: 1, battery: 100, status: "moving" as const, jobId: "J1", blocked: true, waitTicks: 42 };
  return { tick: 42, poisoned: false, layout: { width: 4, height: 4, cells: Array.from({ length: 16 }, (_, i) => ({ x: i % 4, y: Math.floor(i / 4), kind: "free" })) },
    stations: [], robots: [source, { id: "R3", x: 2, y: 1, battery: 98, status: "idle", jobId: "", blocked: false, waitTicks: 0 }],
    activeIncidents: [{ robotId: "R1", type: "traffic_blocked", severity: "high", telemetry: { ...source } }],
    jobs: [{ id: "J1", status: "assigned", assignedRobot: "R1", fromX: 2, fromY: 2, toX: 3, toY: 2 }] };
}
const reassign: Proposal = { id: "P-reassign", action: "reassign_unstarted_job", robotId: "", stationId: "", jobId: "J1", fromRobotId: "R1", toRobotId: "R3",
  state: "pending", execution: "none", submittedBy: "proposer", currentRuntime: true, createdAt: "2026-10-16T12:00:00Z", expiresAt: "2030-10-16T12:00:00Z" };
const charging: Proposal = { ...reassign, id: "P-charge", action: "set_charging", robotId: "R4", stationId: "S1", jobId: "", fromRobotId: "", toRobotId: "" };
const html = (element: React.ReactNode) => renderToString(element).replace(/<!--.*?-->/g, "");
function inbox(receipt: Receipt | null = null, proposals = [reassign, charging], phase: "ready" | "unavailable" | "forbidden" = "ready") {
  return html(<InboxView feed={{ phase, proposals, updatedAt: new Date() }} busy={null} receipt={receipt} runtimeAvailable decide={vi.fn()} refresh={vi.fn()} refreshStatus={vi.fn()} />);
}
afterEach(() => { vi.unstubAllGlobals(); });

describe("incident and job monitoring", () => {
  it("renders Go severity and supporting telemetry without inventing incidents", () => {
    const state = snapshot();
    const output = html(<Dashboard feed={{ phase: "live", snapshot: parseSnapshot(state), updatedAt: new Date() }} />);
    for (const text of ["Active incidents", "R1 · Traffic blocked", "High severity", "Battery 100%", "waiting 42 ticks", "Job J1", "Assigned · awaiting pickup"]) expect(output).toContain(text);
    expect(output).not.toContain("Low battery with an assigned job");
    state.activeIncidents = [];
    expect(html(<Dashboard feed={{ phase: "live", snapshot: state, updatedAt: new Date() }} />)).toContain("No active incidents in this snapshot");
  });
  it("shows low battery, offline and medium severity only when supplied by Go", () => {
    const state = snapshot();
    state.robots[0].battery = 29;
    state.robots[1].status = "offline";
    state.activeIncidents = [ { robotId: "R1", type: "low_battery", severity: "medium", telemetry: { ...state.robots[0] } },
      { robotId: "R3", type: "robot_unresponsive", severity: "high", telemetry: { ...state.robots[1] } } ];
    const output = html(<Dashboard feed={{ phase: "live", snapshot: state, updatedAt: new Date() }} />);
    for (const text of ["Low battery with an assigned job", "Medium severity", "Robot offline"]) expect(output).toContain(text);
  });
  it("shows reassigned ownership, recipient movement, pickup and delivery as distinct snapshots", () => {
    const state = snapshot(); state.activeIncidents = []; state.jobs[0].assignedRobot = "R3";
    Object.assign(state.robots[0], { status: "idle", jobId: "", waitTicks: 0, blocked: false });
    Object.assign(state.robots[1], { status: "moving", jobId: "J1" });
    const render = () => html(<Dashboard feed={{ phase: "live", snapshot: state, updatedAt: new Date() }} />);
    expect(render()).toContain('<th scope="row">J1</th><td>R3'); expect(render()).toContain("moving · (2, 1)");
    expect(render()).toContain('class="robot-job">J1'); expect(render()).not.toContain("Delivered");
    state.tick++; state.robots[1].y = 2; state.jobs[0].status = "in_progress";
    expect(render()).toContain("Picked up · delivering"); expect(render()).toContain("translate(100,98)");
    state.jobs[0].status = "done"; state.robots[1].jobId = ""; state.robots[1].status = "idle";
    expect(render()).toContain("Delivered");
  });
  it("labels stale incidents and jobs and rejects missing or contradictory protocol data", () => {
    const output = html(<Dashboard feed={{ phase: "unavailable", snapshot: snapshot(), updatedAt: new Date() }} />);
    expect(output).toContain("Last received incidents"); expect(output).toContain("progress may be stale");
    const bad = snapshot(); bad.activeIncidents[0].telemetry.battery--;
    expect(() => parseSnapshot(bad)).toThrow();
    for (const value of [{ ...snapshot(), activeIncidents: undefined }, { ...snapshot(), jobs: undefined },
      { ...snapshot(), jobs: [{ ...snapshot().jobs[0], fromX: 99 }] },
      { ...snapshot(), activeIncidents: [...snapshot().activeIncidents, ...snapshot().activeIncidents] }]) expect(() => parseSnapshot(value)).toThrow();
  });
});

describe("both proposal workflows", () => {
  it("renders both typed actions and never labels reassignment as charging", () => {
    const output = inbox();
    for (const text of ["Job J1: R1", "→ R3", "ReassignUnstartedJob", "P-reassign", "pending", "Approval does not complete delivery", "SetCharging", "R4", "S1"]) expect(output).toContain(text);
    for (const proposal of [reassign, charging]) expect(parseProposal(proposal)).toEqual(proposal);
    for (const proposal of [{ ...reassign, jobId: "" }, { ...reassign, toRobotId: "R1" }, { ...reassign, robotId: "R1" }, { ...charging, action: "invented" }]) expect(() => parseProposal(proposal)).toThrow();
  });
  it("shows transfer outcomes independently from delivery and retains rejected/expired/stale errors", () => {
    const receipt: Receipt = { id: reassign.id, proposal: reassign, decision: "approve", latest: { ...reassign, state: "approved", execution: "succeeded" }, refreshing: false, refreshFailed: false,
      result: { proposalId: reassign.id, state: "approved", execution: "succeeded", executionAttempted: true, auditCommitted: true, error: null } };
    expect(inbox(receipt, [])).toContain("Job J1 transferred from R1 to R3"); expect(inbox(receipt, [])).toContain("not delivery completion");
    receipt.result = { ...receipt.result!, executionAttempted: false, error: "proposal already decided" };
    expect(inbox(receipt)).toContain("Previously recorded execution"); expect(inbox(receipt)).toContain("This decision did not execute a command");
    for (const error of ["request denied", "proposal expired", "unauthorized", "self approval forbidden"]) {
      receipt.result = { ...receipt.result!, state: "pending", execution: "none", error };
      expect(inbox(receipt)).toContain(error); expect(inbox(receipt)).not.toContain("Job J1 transferred");
    }
    receipt.result = { ...receipt.result!, state: "rejected", error: null };
    expect(inbox(receipt)).toContain("No command was executed by this decision");
    receipt.result = { ...receipt.result!, execution: "failed", state: "approved", error: "request denied" };
    expect(inbox(receipt)).toContain("The command failed");
    receipt.result = { ...receipt.result!, auditCommitted: false, execution: "unknown" };
    expect(inbox(receipt)).toContain("Decision outcome unknown"); expect(inbox(receipt)).not.toContain("Job J1 transferred");
    receipt.refreshFailed = true;
    expect(inbox(receipt)).toContain("Last received stored status"); expect(inbox(receipt)).toContain("Could not refresh the stored status");
  });
  it("disables expired/foreign/stale decisions and hides both actions from viewer/proposer inboxes", () => {
    for (const p of [{ ...reassign, expiresAt: "2020-01-01T00:00:00Z" }, { ...reassign, currentRuntime: false }]) expect(inbox(null, [p])).toMatch(/class="approve-button" disabled/);
    expect(inbox(null, [reassign], "unavailable")).toMatch(/class="approve-button" disabled/);
    const forbidden = inbox(null, [reassign, charging], "forbidden");
    expect(forbidden).toContain("Fleet monitoring remains available"); expect(forbidden).not.toContain("approve-button"); expect(forbidden).not.toContain("P-reassign");
  });
  it("round-trips typed proposals and both decisions through only the existing decision mutations", async () => {
    const request = vi.fn<typeof fetch>(); vi.stubGlobal("fetch", request);
    request.mockResolvedValueOnce(new Response(JSON.stringify({ data: { pendingProposals: [reassign, charging] } })));
    expect(await fetchPending("test-token", new AbortController().signal)).toEqual([reassign, charging]);
    for (const p of [reassign, charging]) for (const decision of ["approve", "reject"] as const) {
      const operation = decision === "approve" ? "approveProposal" : "rejectProposal";
      const result = { proposalId: p.id, state: decision === "approve" ? "approved" : "rejected", execution: decision === "approve" ? "succeeded" : "none", executionAttempted: decision === "approve", auditCommitted: true, error: null };
      request.mockResolvedValueOnce(new Response(JSON.stringify({ data: { [operation]: result } })));
      expect(await decideProposal("test-token", p.id, decision, new AbortController().signal)).toEqual(result);
    }
    request.mockResolvedValueOnce(new Response(JSON.stringify({ data: { proposal: { ...reassign, id: "wrong" } } })));
    await expect(fetchProposal("test-token", reassign.id, new AbortController().signal)).rejects.toThrow();
    for (const [, options] of request.mock.calls) expect(options?.body).not.toMatch(/submitReassignment|submitProposal|inject|Tick|Commander/);
  });
  it("treats malformed or contradictory execution responses as uncertain instead of successful", async () => {
    const request = vi.fn<typeof fetch>(); vi.stubGlobal("fetch", request);
    const result = { proposalId: reassign.id, state: "approved", execution: "succeeded", executionAttempted: true, auditCommitted: true, error: null };
    for (const invalid of [{ ...result, execution: "delivered" }, { ...result, state: "invented" }, { ...result, state: "pending" }]) {
      request.mockResolvedValueOnce(new Response(JSON.stringify({ data: { approveProposal: invalid } })));
      await expect(decideProposal("test-token", reassign.id, "approve", new AbortController().signal)).rejects.toThrow();
    }
  });
});
