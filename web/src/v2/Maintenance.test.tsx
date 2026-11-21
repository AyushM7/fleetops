import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { Fleet } from "./Fleet";
import { RobotMaintenanceDetails, WarehouseMaintenance } from "./Maintenance";
import { DecisionDetails } from "./OperationsAI";
import type { Job, MaintenanceAttempt, OperationsActivity, Robot, Snapshot } from "./telemetry";
import { acceptedMaintenanceAttempt, maintenanceJobResult } from "./warehousePresentation";

const robot: Robot = {
  id: "R4", x: 9, y: 9, status: "faulted", activeFault: "sensor_failure", activity: "FAULTED",
  role: "transport", battery: 59, jobId: "J4", blocked: false, waitTicks: 0,
};
const m1: Robot = {
  ...robot, id: "M1", y: 10, status: "servicing", activity: "REPAIRING", activeFault: undefined,
  role: "maintenance", jobId: "",
};
const job: Job = {
  id: "J4", status: "in_progress", assignedRobot: "R4", cargoRobotId: "R4", type: "NORMAL",
  from: { x: 6, y: 1 }, to: { x: 17, y: 10 },
};
const attempt: MaintenanceAttempt = {
  id: "MA1", robotId: "R4", faultId: "F1", fault: "sensor_failure", jobId: "J4",
  targetCell: { x: 9, y: 9 }, serviceCell: { x: 9, y: 10 }, startedTick: 49, repairTicks: 3,
  requiredRepairTicks: 3,
};
const verifying: Snapshot = {
  tick: 107, seed: 42, robots: [robot, m1], jobs: [job], stations: [], aisles: [],
  faults: [{ id: "F1", type: "sensor_failure", robots: ["R4"], tick: 48 }],
  maintenance: { phase: "verifying", active: attempt },
};
const receipt: OperationsActivity = {
  observationId: "P" + "1".repeat(32), observedAt: "2026-11-19T00:00:00Z",
  tick: 48, stage: "finished", status: "applied", recorded: true,
  decision: { kind: "maintenance", maintenance: { robotId: "R4", faultId: "F1" } },
  go: { execution: "succeeded", auditCommitted: true, auditId: 7 },
};
const inspect = vi.fn();

describe("V2 maintenance telemetry", () => {
  it("shows stationary work and verification separately, with deterministic provenance", () => {
    const html = renderToStaticMarkup(<RobotMaintenanceDetails robot={m1} snapshot={verifying} inspect={inspect} />);
    expect(html).toContain("MA1");
    expect(html).toContain("Verifying recovery");
    expect(html).toContain("Deterministic Go");
    expect(html).toContain('max="3" value="3"');
    expect(html).toContain('aria-label="Inspect job J4"');
    expect(html).not.toContain("Recovery verified");
  });

  it("distinguishes two occurrences and includes only their live blockers", () => {
    const second: Robot = { ...robot, id: "R7", x: 6, activeFault: "critical_battery", battery: 0 };
    const snapshot: Snapshot = {
      ...verifying, robots: [...verifying.robots, second],
      faults: [...verifying.faults, { id: "F2", type: "critical_battery", robots: ["R7"], tick: 85 }],
      maintenance: { ...verifying.maintenance!, issues: [{ robotId: "R7", faultId: "F2", reason: "external_power_required" }] },
    };
    const html = renderToStaticMarkup(<WarehouseMaintenance snapshot={snapshot} inspect={inspect} />);
    expect(html).toContain("F1");
    expect(html).toContain("F2");
    expect(html).toContain("External power required");
    expect(html).toContain("Verifying recovery");
  });

  it("links a durable receipt only to the matching, AI-directed attempt", () => {
    const html = renderToStaticMarkup(<DecisionDetails activity={receipt} snapshot={verifying} inspect={inspect} />);
    expect(html).toContain("Go admitted the maintenance dispatch");
    expect(html).not.toContain("Simulator maintenance attempt");
    const ai = { ...verifying, maintenance: { phase: "verifying" as const, active: { ...attempt, aiDirected: true } } };
    expect(acceptedMaintenanceAttempt(ai, receipt)?.id).toBe("MA1");
    expect(acceptedMaintenanceAttempt({
      ...ai, maintenance: { phase: "verifying", active: { ...ai.maintenance.active, faultId: "F2" } },
    }, receipt)).toBeUndefined();
    expect(acceptedMaintenanceAttempt(ai, { ...receipt, status: "go_rejected" })).toBeUndefined();
    expect(acceptedMaintenanceAttempt(ai, { ...receipt, stage: "proposed" })).toBeUndefined();
  });

  it.each([
    ["failed", "Repair failed"], ["unresolved", "Repair unresolved"], ["cancelled", "Attempt cancelled"],
  ] as const)("keeps a %s result and the current breakdown visible", (outcome, label) => {
    const snapshot: Snapshot = { ...verifying, maintenance: { phase: "returning", history: [{
      ...attempt, outcome, finishedTick: 107, reason: "target_recovery_blocked",
    }] } };
    const html = renderToStaticMarkup(<RobotMaintenanceDetails robot={robot} snapshot={snapshot} inspect={inspect} />);
    expect(html).toContain("Sensor failure");
    expect(html).toContain("Repair attempt already recorded");
    expect(html).toContain("Recovery verification blocked");
    expect(html).toContain(label);
    expect(html).not.toContain("Recovery verified");
  });

  it("links recovered custody and subsequent delivery only to the interrupted job", () => {
    const completed: MaintenanceAttempt = { ...attempt, outcome: "completed", finishedTick: 108 };
    const recovered: Robot = { ...robot, activeFault: undefined, status: "moving", activity: "DELIVERING" };
    const after: Snapshot = {
      ...verifying, tick: 109, robots: [recovered, m1], maintenance: { phase: "returning", history: [completed] },
    };
    expect(maintenanceJobResult(after, completed)).toBe("Delivery resumed");
    const delivered: Snapshot = {
      ...after, tick: 122, jobs: [{ ...job, status: "done", cargoRobotId: undefined, completedTick: 122 }],
    };
    expect(maintenanceJobResult(delivered, completed)).toBe("Delivered at tick 122");
    expect(maintenanceJobResult(delivered, { ...completed, jobId: "J5" })).toBeUndefined();
    const legacy = { ...completed, jobId: undefined };
    expect(maintenanceJobResult(delivered, legacy)).toBeUndefined();
    const legacyDetails = renderToStaticMarkup(<RobotMaintenanceDetails robot={recovered}
      snapshot={{ ...delivered, maintenance: { phase: "returning", history: [legacy] } }} inspect={inspect} />);
    expect(legacyDetails).toContain("Recovery verified");
    expect(legacyDetails).not.toContain("Interrupted job");
    expect(legacyDetails).not.toContain("Delivered at tick");
    const html = renderToStaticMarkup(<Fleet snapshot={delivered} selection={null} pick={inspect} />);
    expect(html).toContain("Recovery verified");
    expect(html).not.toContain("Sensor failure");
  });

  it("shows the bay arrival from M1's parked activity while the last attempt is returning", () => {
    const snapshot: Snapshot = {
      ...verifying, tick: 139, robots: [robot, { ...m1, y: 0, status: "idle", activity: "PARKED" }],
      maintenance: { phase: "returning", history: [{ ...attempt, outcome: "completed", finishedTick: 108 }] },
    };
    const html = renderToStaticMarkup(<WarehouseMaintenance snapshot={snapshot} inspect={inspect} />);
    expect(html).toContain("M1 · Parked");
  });
});
