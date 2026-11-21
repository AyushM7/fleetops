import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { DecisionDetails, OperationsAI } from "./OperationsAI";
import type { WarehouseFeed } from "./session";
import type { OperationsActivity, Snapshot, WarehouseFrame } from "./telemetry";

const snapshot: Snapshot = {
  tick: 28, seed: 1, aisles: [], stations: [],
  robots: [{ id: "R1", role: "transport", x: 0, y: 0, status: "idle", activity: "AVAILABLE", battery: 90, jobId: "", blocked: false, waitTicks: 0 }],
  jobs: [{ id: "J1", type: "PRIORITY", status: "done", assignedRobot: "R1",
    from: { x: 1, y: 1 }, to: { x: 2, y: 2 }, completedTick: 25, outcome: "ON_TIME" }],
  faults: [{ id: "F1", type: "low_battery", tick: 13, robots: ["R1"] }],
};
const frame: WarehouseFrame = {
  generation: "a".repeat(64), runtimeId: "P" + "1".repeat(32), eventId: 30, type: "snapshot",
  layout: { width: 20, height: 13, cells: [] }, state: "running", mode: "deterministic", operations: [], snapshot,
};
const live: WarehouseFeed = { phase: "live", frame, status: null, pending: null, controlWarning: null };
const observed: OperationsActivity = {
  observationId: "P" + "2".repeat(32), observedAt: "2026-11-19T00:00:00Z", tick: 13,
  stage: "observed", status: "observed", recorded: false,
};
const proposed: OperationsActivity = {
  ...observed, observationId: "P" + "3".repeat(32), tick: 20, stage: "proposed", status: "proposed",
  recorded: true, modelStatus: "decided", proposedAt: "2026-11-19T00:00:01Z",
  decision: { kind: "reassign", reassignment: { JobID: "J2", FromRobotID: "R1", ToRobotID: "R2" } },
  requestId: 18, reservedMicroUsd: 289384, accountedMicroUsd: 0,
  usage: { model: "fixture-provider-model", inputTokens: 18, outputTokens: 0 },
};

const page = (feed = live) => renderToStaticMarkup(<OperationsAI feed={feed} inspect={vi.fn()} />);
const details = (activity: OperationsActivity) => renderToStaticMarkup(<DecisionDetails activity={activity} snapshot={snapshot} inspect={vi.fn()} />);
const withActivities = (...operations: OperationsActivity[]) => page({ ...live, frame: { ...frame, operations } });

describe("Operations AI with telemetry fixtures", () => {
  it("shows real warehouse outcomes without implying model work or offering a mutation", () => {
    const html = page();
    for (const text of ["Deterministic dispatch", "No decision activity recorded", "Job delivered", "On time", "Low battery recorded", "Go telemetry", "J1", "Tick 25", "Tick 13"]) {
      expect(html).toContain(text);
    }
    expect(html.indexOf("Job delivered")).toBeLessThan(html.indexOf("Low battery recorded"));
    expect(html).not.toContain('data-tone="assisted"');
    expect(html).not.toMatch(/>Approve|>Reject</);
    expect(html).toContain("Go validates and executes V2 dispatch commands automatically. Scripted proposals in the V1 demo require operator approval.");
  });

  it("shows the latest cycle first and distinguishes observed, reserved and decided activity", () => {
    const budget = { ...observed, stage: "finished", status: "budget_denied" } as const;
    const reserved = { ...proposed, decision: undefined, modelStatus: "model_unavailable", model: "fixture-configured-model",
      usage: undefined, status: "model_unavailable", stage: "finished" } as const;
    const html = withActivities(budget, proposed);
    expect(html.indexOf("Proposed to Go")).toBeLessThan(html.indexOf("Budget denied"));
    expect(html).toContain('data-tone="assisted">Model decision');
    expect(html).toContain("Go observation");
    expect(html).toContain("Observed at tick 20");
    for (const entry of [budget, reserved, observed]) expect(withActivities(entry)).not.toContain('data-tone="assisted"');
    expect(withActivities(reserved)).toContain("Model response unconfirmed");
    expect(withActivities(observed)).toContain("Checking assistance");
  });

  it("shows journal, recommendation, accessible references and verified usage without claiming execution", () => {
    const html = details(proposed);
    for (const text of ["Confirmed", proposed.observationId, "Candidate previews", "Proposal prepared", "Awaiting result",
      "J2 · Outside current snapshot", 'aria-label="Inspect robot R1"', "R2 · Outside current snapshot",
      "fixture-provider-model", "18 in / 0 out", "Ledger request", "Reserved (USD)", "Accounted (USD)", "0.289384", "$0.00"]) {
      expect(html).toContain(text);
    }
    expect(html).not.toContain("The receipt confirms");
  });

  it("requires a committed successful receipt to call a command applied", () => {
    const applied: OperationsActivity = { ...proposed, stage: "finished", status: "applied", go: { execution: "succeeded", auditId: 7, auditCommitted: true } };
    expect(withActivities(applied)).toContain("Applied by Go");
    const receipt = details(applied);
    expect(receipt).toContain("Go validation &amp; automatic execution");
    expect(receipt).toContain("The receipt confirms the dispatch change");
    expect(receipt).not.toMatch(/approv/i);
    expect(receipt).toContain("<dt>Audit receipt</dt><dd>7</dd>");
    const uncertain: OperationsActivity = { ...applied, go: { execution: "succeeded", auditId: 7, auditCommitted: false } };
    expect(withActivities(uncertain)).toContain("Go execution unconfirmed");
    expect(withActivities(uncertain)).not.toContain("Applied by Go");
    expect(details(uncertain)).not.toContain("The receipt confirms");
    const rejected: OperationsActivity = { ...applied, status: "go_rejected", goReason: "stale_observation",
      go: { auditId: 8, execution: "none", auditCommitted: true } };
    expect(withActivities(rejected)).toContain("Rejected by Go");
    expect(details(rejected)).toContain("Observation became stale");
    expect(details(rejected)).toContain("No execution");
  });

  it("shows an abstention and keeps missing model or ledger data unavailable", () => {
    const abstained: OperationsActivity = { ...observed, stage: "finished", status: "abstained", modelStatus: "decided",
      decision: { kind: "abstain", reason: "no_safe_option" } };
    expect(withActivities(abstained)).toContain("No action recommended");
    const html = details(abstained);
    for (const text of ["No safe option", "No command receipt", "Unconfirmed", "<dt>Token usage</dt><dd>Unavailable</dd>",
      "<dt>Accounted (USD)</dt><dd>Unavailable</dd>"]) expect(html).toContain(text);
  });

  it("shows maintenance admission as a dispatch with a separate physical outcome", () => {
    const dispatch: OperationsActivity = {
      ...proposed, stage: "finished", status: "applied",
      decision: { kind: "maintenance", maintenance: { robotId: "R1", faultId: "F1" } },
      go: { auditId: 7, execution: "succeeded", auditCommitted: true },
    };
    expect(withActivities(dispatch)).toContain("Maintenance dispatch");
    const html = details(dispatch);
    expect(html).toContain('aria-label="Inspect robot R1"');
    expect(html).toContain("<code>F1</code>");
    expect(html).toContain("Go admitted the maintenance dispatch. The simulator records the repair outcome separately.");
  });

  it("separates a loading or unavailable feed from empty and retained data", () => {
    expect(page({ ...live, phase: "connecting", frame: null })).toContain("Waiting for confirmed operations telemetry");
    const unavailable = page({ ...live, phase: "unavailable", frame: null });
    expect(unavailable).toContain("The warehouse run is unavailable");
    expect(unavailable).not.toContain("No decision activity");
    const stale = page({ ...live, phase: "reconnecting" });
    expect(stale).toContain("Showing last confirmed data at tick 28");
    expect(stale).toContain("Job delivered");
    expect(page({ ...live, phase: "expired", frame: null })).not.toContain("Job delivered");
  });
});
