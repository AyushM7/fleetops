import { z } from "zod";

const position = { x: z.number().int(), y: z.number().int() };
export const robotSchema = z.object({
  id: z.string().min(1), ...position, battery: z.number().int().min(0).max(100),
  status: z.string(), jobId: z.string(), waitTicks: z.number().int(), blocked: z.boolean(),
});
export const incidentSchema = z.object({
  robotId: z.string().min(1), type: z.enum(["low_battery", "robot_unresponsive", "traffic_blocked"]),
  severity: z.enum(["medium", "high"]), telemetry: robotSchema,
});
export type ActiveIncident = z.infer<typeof incidentSchema>;
export const stationSchema = z.object({ id: z.string().min(1), ...position, occupiedBy: z.string(), reservedBy: z.string() });
export const fleetSchema = z.object({
  tick: z.number().int().nonnegative(), poisoned: z.boolean(),
  robots: z.array(robotSchema), activeIncidents: z.array(incidentSchema),
  stations: z.array(stationSchema),
});
export type FleetState = z.infer<typeof fleetSchema>;
export const chargingSchema = z.object({
  robotId: z.string().min(1).max(128), stationId: z.string().min(1).max(128), expiresAt: z.iso.datetime({ offset: true }),
});
export type ChargingInput = z.infer<typeof chargingSchema>;
export const mutationSchema = z.object({
  proposalId: z.string(), state: z.string(), execution: z.string(),
  auditCommitted: z.boolean(), executionAttempted: z.boolean(), error: z.string().nullable(),
});
export type MutationResult = z.infer<typeof mutationSchema>;
export const analysisSchema = z.object({
  observations: z.array(z.string()), potentialIssues: z.array(z.string()),
  recommendedAction: z.object({ action: z.literal("SetCharging"), robotId: z.string(), stationId: z.string() }).nullable(),
  explanation: z.string(),
});
export type Analysis = z.infer<typeof analysisSchema>;
export type ProposalStatus = {
  status: "disabled" | "not_requested" | "pending_approval" | "rejected" | "unknown";
  proposalId?: string;
};
export type FleetObservation = Analysis & {
  incidents: ActiveIncident[];
  operatorAttention: { robotId: string; incidentType: ActiveIncident["type"]; reason: string }[];
};
export const snapshotReferenceSchema = z.object({
  scope: z.literal("snapshot"), tick: z.number().int().nonnegative(),
  robotId: z.string().min(1), type: incidentSchema.shape.type,
}).strict();
export type SnapshotReference = z.infer<typeof snapshotReferenceSchema>;
export const reasoningSchema = z.object({
  tick: z.number().int().nonnegative(),
  interpretations: z.array(z.object({
    reference: snapshotReferenceSchema, severity: incidentSchema.shape.severity,
    telemetry: robotSchema.strict(), assessment: z.string().min(1).max(1200),
    recommendedResponse: z.literal("operator_attention"),
  }).strict()).max(300),
  proposedAction: z.object({
    action: z.literal("SetCharging"), robotId: z.string().min(1), stationId: z.string().min(1),
    purpose: z.enum(["routine_charging", "demonstration"]), rationale: z.string().min(1).max(1200),
    evidence: z.object({ tick: z.number().int().nonnegative(), robot: robotSchema.strict(), station: stationSchema.strict() }).strict(),
  }).strict().nullable(),
}).strict();
export type AIReasoning = z.infer<typeof reasoningSchema>;
export type AIAnalysis = {
  status: "disabled" | "accepted" | "rejected" | "failed";
  interpretations: AIReasoning["interpretations"];
  proposedAction: AIReasoning["proposedAction"];
};
export type SubmissionCheck = "disabled" | "not_requested" | "current" | "snapshot_changed" | "unavailable";
export type AgentResult = FleetObservation & {
  observedFacts: { referenceScope: "snapshot"; referenceNotice: string; fleet: FleetState; incidentReferences: SnapshotReference[] };
  aiAnalysis: AIAnalysis; submissionCheck: SubmissionCheck; proposal: ProposalStatus;
};
