import type { JSX } from "react";
import type { DemoStatus } from "./restart";

const stages = ["observe", "incident", "analyze", "decide", "outcome"] as const;

export function guidedMessage(status: DemoStatus): string {
  const guide = status.guide;
  const fleet = status.snapshot;
  if (!guide || !fleet) return "Waiting for the guided simulation.";
  if (!status.ready || status.state === "unavailable") return "Guided run unavailable. Refresh status, then restart or reconcile any unconfirmed proposal outcome.";
  const r1 = fleet.robots.find(robot => robot.id === "R1");
  const job = fleet.jobs.find(job => job.id === "J1");
  switch (guide.stage) {
    case "observe": return "Seeded warehouse simulation. R2 is already offline and J1 belongs to R1. Press Play to observe the accumulating wait.";
    case "incident": return `R1 has waited ${r1?.waitTicks ?? 0} ticks. Go reports traffic blockage; R2 was offline from initialization. The controller will pause at the verified decision point.`;
    case "analyze": return "Verified intervention state reached. Time is paused while the deterministic local adapter prepares a Go-validated proposal. No live LLM.";
    case "decide": return guide.decision === "expired" ? "The proposal expired by wall-clock time. Restart for a fresh proposal; ticking remains blocked." : "Review J1: R1 → R3. Go validates; you approve or reject. Simulation time remains paused.";
    case "outcome":
      if (guide.decision === "rejected") return `Rejected · no reassignment executed. J1 is ${job?.status ?? "unconfirmed"} on ${job?.assignedRobot || "unconfirmed"}. Resume or Step to observe the no-intervention path.`;
      if (guide.decision === "approved" && guide.deliveryTick !== undefined && guide.decisionTick !== undefined && job?.status === "done" && job.assignedRobot === "R3") return `Delivered · Go confirms J1 on R3 at tick ${guide.deliveryTick}, ${guide.deliveryTick - guide.decisionTick} simulator ticks after the intervention point.`;
      return "Transfer recorded; delivery is not confirmed. Resume or Step to observe the actual pickup and delivery.";
  }
}

export default function GuidedExperience({ status, unavailable }: { status: DemoStatus; unavailable: boolean }): JSX.Element | null {
  if (!status.guide) return null;
  return <div className="guided-experience" aria-label="Guided simulation">
    <ol aria-label="Walkthrough stages">{stages.map(stage => <li key={stage} aria-current={stage === status.guide?.stage ? "step" : undefined}>{stage.charAt(0).toUpperCase() + stage.slice(1)}</li>)}</ol>
    <p role="status">{unavailable ? "Last received guided stage; current state is unconfirmed." : guidedMessage(status)}</p>
    <details><summary>Scenario facts</summary><p>Trusted seed 42, same 8×8 warehouse. R2 is offline and R1 owns uncollected J1 from tick 0. R1 cannot leave the pocket; its wait grows, Go later flags blockage, and idle batteries drain. No healthy-traffic opening or newly occurring fault is implied. Positions remain where Go reports them. At the verified tick-42 state, the local adapter proposes one transfer; only the independent operator can approve. Restart repeats the selected mode. R2 can remain offline after delivery.</p>{status.guide.proposalId && <p>Current proposal: <code>{status.guide.proposalId}</code></p>}</details>
  </div>;
}
