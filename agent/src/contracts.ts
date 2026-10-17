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
export const fleetSchema = z.object({
  tick: z.number().int().nonnegative(), poisoned: z.boolean(),
  robots: z.array(robotSchema), activeIncidents: z.array(incidentSchema),
  stations: z.array(z.object({ id: z.string().min(1), ...position, occupiedBy: z.string(), reservedBy: z.string() })),
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
export type AgentResult = FleetObservation & { proposal: ProposalStatus };
