import { renderToString } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { InboxView, type Receipt } from "./ApprovalInbox";
import type { InboxFeed, Proposal } from "./proposals";
const proposal: Proposal = { id: "P-example", action: "set_charging", robotId: "R1", stationId: "S1", state: "pending", execution: "none", currentRuntime: true, submittedBy: "proposer", createdAt: "2026-10-16T12:00:00Z", expiresAt: "2030-10-16T12:00:00Z" };
const ready: InboxFeed = { phase: "ready", proposals: [proposal], updatedAt: new Date() };
function render(feed: InboxFeed = ready, receipt: Receipt | null = null, busy: string | null = null, runtimeAvailable = true) {
  return renderToString(<InboxView feed={feed} receipt={receipt} busy={busy} runtimeAvailable={runtimeAvailable} decide={vi.fn()} refresh={vi.fn()} refreshStatus={vi.fn()} />).replace(/<!--.*?-->/g, "");
}
describe("operator inbox", () => {
  it("shows stored proposal fields, absent rationale and approval/rejection controls", () => {
    const html = render(); for (const value of ["P-example", "R1", "S1", "SetCharging", "pending", "No rationale recorded", "Approve", "Reject"]) expect(html).toContain(value);
  });
  it("has no proposal data or decision controls for non-operators", () => {
    const html = render({ ...ready, phase: "forbidden", proposals: [] });
    expect(html).toContain("Operator access required"); expect(html).not.toContain("approve-button"); expect(html).not.toContain("P-example");
  });
  it("handles loading, empty lists, stale proposals and disabled decisions", () => {
    expect(render({ ...ready, phase: "loading", proposals: [] })).toContain("Loading pending proposals");
    expect(render({ ...ready, proposals: [] })).toContain("No pending proposals");
    for (const html of [render({ ...ready, phase: "unavailable" }), render(ready, null, "P-example"), render(ready, null, null, false)]) expect(html).toMatch(/class="approve-button" disabled/);
    expect(render({ ...ready, phase: "unavailable" })).toContain("Displayed proposals may be stale");
    expect(render({ ...ready, proposals: [{ ...proposal, expiresAt: "2020-01-01T00:00:00Z" }] })).toContain("Expiration time has passed");
  });
  it("displays real duplicate and expiration denials with historical execution flags", () => {
    for (const error of ["proposal already decided", "proposal expired", "proposal belongs to a prior runtime", "self approval forbidden", "request denied"]) {
      const receipt: Receipt = { id: proposal.id, decision: "approve", result: { proposalId: proposal.id, state: "approved", execution: "succeeded", auditCommitted: true, executionAttempted: false, error }, latest: { ...proposal, state: "approved", execution: "succeeded" }, refreshing: false, refreshFailed: false };
      const html = render(ready, receipt); expect(html).toContain(error); expect(html).toContain("Decision declined"); expect(html).toContain("Latest stored status"); expect(html).toContain("Execution attempted this call</dt><dd>No");
    }
  });
  it("does not claim durable success for missing or uncommitted responses", () => {
    const receipt: Receipt = { id: proposal.id, decision: "approve", result: null, latest: null, refreshing: false, refreshFailed: true };
    expect(render(ready, receipt)).toContain("Decision outcome unknown"); expect(render(ready, receipt)).toContain("Do not assume execution failed or retry automatically");
    receipt.result = { proposalId: proposal.id, state: "", execution: "unknown", auditCommitted: false, executionAttempted: true, error: "workflow unavailable" };
    expect(render(ready, receipt)).toContain("Decision outcome unknown");
    expect(render(ready, receipt)).toContain("Not confirmed"); expect(render(ready, receipt)).toContain("workflow unavailable");
  });
});
