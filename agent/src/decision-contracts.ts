import { z } from "zod";
import { candidateSchema, incidentSchema, jobSchema, observationIdSchema, robotSchema, snapshotReferenceSchema } from "./contracts.js";

export const decisionObjectiveSchema = z.enum(["compare_tradeoffs", "minimize_pickup_steps", "preserve_battery_headroom"]);
export type DecisionObjective = z.infer<typeof decisionObjectiveSchema>;
export const candidateKeySchema = z.object({ jobId: z.string().min(1), fromRobotId: z.string().min(1), toRobotId: z.string().min(1) }).strict();
export type CandidateKey = z.infer<typeof candidateKeySchema>;
const integer = z.number().int().nonnegative();
export const decisionMetricSchema = z.enum(["pickupSteps", "deliverySteps", "chargingExitSteps", "totalRouteSteps", "battery", "requiredBattery", "missionHeadroom", "admissionHeadroom"]);
export type DecisionMetric = z.infer<typeof decisionMetricSchema>;
export const candidateEvidenceSchema = z.object({
  candidate: candidateSchema.strict(), source: robotSchema.strict(), recipient: robotSchema.strict(), job: jobSchema.strict(),
  incidentReference: snapshotReferenceSchema,
  totalRouteSteps: integer, missionHeadroom: integer, admissionHeadroom: integer,
}).strict();
export type CandidateEvidence = z.infer<typeof candidateEvidenceSchema>;
export const comparisonSchema = z.object({
  left: candidateKeySchema, right: candidateKeySchema, metric: decisionMetricSchema,
  leftValue: integer, relation: z.enum(["less", "equal", "greater"]), rightValue: integer,
}).strict();
export type CandidateComparison = z.infer<typeof comparisonSchema>;
export const decisionLimitations = ["snapshot_only", "routes_not_reserved", "obstruction_remains", "delivery_not_guaranteed", "independent_approval_required", "optimality_not_established"] as const;
export const abstentionReasonSchema = z.enum(["runtime_poisoned", "no_candidates", "no_supported_incident", "operator_tradeoff"]);
export type AbstentionReason = z.infer<typeof abstentionReasonSchema>;
export const recoveryDecisionSchema = z.object({
  version: z.literal("recovery-decision-v2"), observationId: observationIdSchema, tick: integer, objective: decisionObjectiveSchema,
  incidents: z.array(z.object({ reference: snapshotReferenceSchema, severity: incidentSchema.shape.severity, telemetry: robotSchema.strict() }).strict()).max(300),
  considered: z.array(candidateEvidenceSchema).max(300), comparisons: z.array(comparisonSchema).max(2400),
  decision: z.discriminatedUnion("kind", [
    z.object({ kind: z.literal("select"), selected: candidateKeySchema,
      basis: z.enum(["pickup_cost", "battery_headroom", "tradeoff", "admitted_option"]) }).strict(),
    z.object({ kind: z.literal("abstain"), reason: abstentionReasonSchema }).strict(),
  ]),
  operatorAttention: z.array(snapshotReferenceSchema).max(300),
  limitations: z.array(z.enum(decisionLimitations)).max(decisionLimitations.length),
}).strict();
export type RecoveryDecision = z.infer<typeof recoveryDecisionSchema>;
