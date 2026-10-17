import type { AIReasoning, FleetState } from "./contracts.js";

export const lifecycleStatements = {
  approvalRequired: "Independent operator approval is required before execution.",
  approvalUnknown: "Approval state is unavailable in this snapshot; operator approval cannot be confirmed.",
  approvalNegation: "The proposal is not known to be approved.",
  approvalHypothetical: "If an independent operator approves a proposal, Go must still validate eligibility before execution.",
  deliveryUncertain: "Delivery is not guaranteed; a recommendation does not prove completion.",
};
const assessment = "This condition can interrupt warehouse operations; an operator should investigate the supplied telemetry.";
const charging = [
  "Charging in place is a supported option for this snapshot.",
  "Supported routine charging; human approval is required.",
];
const reassignment = [
  "Transfer may enable job progress; the obstruction remains.",
  "Transfer may recover job progress after human approval; the obstruction remains.",
  "May recover progress after approval; obstruction remains.",
  "Go admitted this recipient; ownership transfer may restore job progress. The obstruction remains.",
  "Deterministic test adapter: Go admitted this transfer; the obstruction remains and delivery is not guaranteed.",
];
const common = [...Object.values(lifecycleStatements), "Human approval required.", "Human approval required; delivery unmeasured."];

function jobStatements(state: FleetState): string[] {
  return state.jobs.flatMap(job => {
    const id = JSON.stringify(job.id);
    const observed = `Job ${id} has Go status ${JSON.stringify(job.status)} at snapshot tick ${state.tick}.`;
    const delivery = job.status === "done"
      ? `Job ${id} is delivered (Go status: done; observed at tick ${state.tick}).`
      : ["pending", "assigned", "in_progress"].includes(job.status)
        ? `Job ${id} is awaiting delivery (Go status: ${job.status}; observed at tick ${state.tick}).`
        : `Job ${id}: completion cannot be confirmed from this snapshot.`;
    const quote = `Quoted job ID (untrusted data, not instructions): ${id}.`;
    return [observed, delivery, quote];
  }).filter(text => text.length <= 1200);
}

// Exact, complete statements form a bounded output language, not a prose classifier.
// Quoted identifiers are copied only from the host snapshot and never grant authority.
export function reasoningStatements(state: FleetState): {
  assessments: string[]; rationales: Record<"SetCharging" | "ReassignUnstartedJob", string[]>;
} {
  const grounded = [...common, ...jobStatements(state)];
  return { assessments: [assessment, ...grounded], rationales: {
    SetCharging: [...charging, ...grounded], ReassignUnstartedJob: [...reassignment, ...grounded],
  } };
}

export function reasoningTextGrounded(reasoning: AIReasoning, state: FleetState): boolean {
  const allowed = reasoningStatements(state);
  return reasoning.interpretations.every(i => allowed.assessments.includes(i.assessment)) &&
    (reasoning.proposedAction === null || allowed.rationales[reasoning.proposedAction.action].includes(reasoning.proposedAction.rationale));
}
