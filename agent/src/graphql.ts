import { z } from "zod";
import { agentRunHandleSchema, agentReceiptSchema, type AgentRunMetadata, type AgentRunHandle, type AgentRunFinish, type AgentRunReceipt } from "./agent-run.js";
import { boundChargingSchema, boundReassignmentSchema, fleetSchema, observationIdSchema, mutationSchema, type BoundChargingInput, type BoundReassignmentInput, type FleetState, type MutationResult } from "./contracts.js";

export const fleetQuery = `query FleetState { fleetState {
  observationId tick poisoned robots { id x y battery status jobId waitTicks blocked }
  stations { id x y occupiedBy reservedBy }
  activeIncidents { robotId type severity telemetry { id x y battery status jobId waitTicks blocked } }
  jobs { id status assignedRobot fromX fromY toX toY }
  reassignmentCandidates { jobId fromRobotId toRobotId pickupSteps deliverySteps chargingExitSteps requiredBattery }
} }`;
export const submitMutation = `mutation SubmitCharging($input: SetChargingInput!) {
  submitProposal(input: $input) { proposalId state execution auditCommitted executionAttempted error }
}`;
export const reassignMutation = `mutation SubmitReassignment($input: ReassignUnstartedJobInput!) {
  submitReassignment(input: $input) { proposalId state execution auditCommitted executionAttempted error }
}`;
export class FleetError extends Error {
  constructor(public readonly code: "configuration" | "authentication" | "unavailable" | "malformed" | "graphql") {
    super(`Fleet API: ${code}`);
  }
}
export interface FleetAPI {
  startAgentRun?(observationId: string, metadata: AgentRunMetadata): Promise<AgentRunHandle>;
  finishAgentRun?(id: string, completion: AgentRunFinish): Promise<AgentRunReceipt>;
  readFleet(): Promise<FleetState>;
  submitCharging(input: BoundChargingInput): Promise<MutationResult>;
  submitReassignment(input: BoundReassignmentInput): Promise<MutationResult>;
}
export function createFleetClient(endpoint: string, token: string, transport: typeof fetch = fetch): FleetAPI {
  let url: URL;
  try { url = new URL(endpoint); } catch { throw new FleetError("configuration"); }
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if (url.username || url.password || url.search || url.hash ||
      !(url.protocol === "https:" || (url.protocol === "http:" && local)) ||
      !/^[A-Za-z0-9_-]{32,512}$/.test(token)) throw new FleetError("configuration");
  async function request<T>(query: string, variables: object, schema: z.ZodType<T>): Promise<T> {
    let response: Response;
    try {
      response = await transport(url, {
        method: "POST", redirect: "error", signal: AbortSignal.timeout(10_000),
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify({ query, variables }),
      });
    } catch { throw new FleetError("unavailable"); }
    if (response.status === 401 || response.status === 403) throw new FleetError("authentication");
    if (!response.ok) throw new FleetError("unavailable");
    let body: unknown;
    try { body = await response.json(); } catch { throw new FleetError("malformed"); }
    const envelope = z.object({ data: z.unknown().optional(), errors: z.array(z.unknown()).optional() }).safeParse(body);
    if (!envelope.success) throw new FleetError("malformed");
    if (envelope.data.errors?.length) throw new FleetError("graphql");
    const parsed = schema.safeParse(envelope.data.data);
    if (!parsed.success) throw new FleetError("malformed");
    return parsed.data;
  }
  return {
    async startAgentRun(observationId, metadata) {
      return (await request(`mutation StartAgentRun($observation: String!, $metadata: String!) {
        startAgentRun(expectedObservationId: $observation, metadata: $metadata) { id runtimeId observationId }
      }`, { observation: observationId, metadata: JSON.stringify(metadata) }, z.object({ startAgentRun: agentRunHandleSchema }))).startAgentRun;
    },
    async finishAgentRun(id, completion) {
      return (await request(`mutation FinishAgentRun($id: ID!, $completion: String!) {
        finishAgentRun(id: $id, completion: $completion) { runId recorded denial proposalResult { proposalId state execution auditCommitted executionAttempted error } }
      }`, { id, completion: JSON.stringify(completion) }, z.object({ finishAgentRun: agentReceiptSchema }))).finishAgentRun;
    },
    async readFleet() { return (await request(fleetQuery, {}, z.object({ fleetState: fleetSchema.extend({ observationId: observationIdSchema }) }))).fleetState; },
    async submitCharging(input) {
      const parsed = boundChargingSchema.safeParse(input);
      if (!parsed.success) throw new FleetError("configuration");
      return (await request(submitMutation, { input: parsed.data }, z.object({ submitProposal: mutationSchema }))).submitProposal;
    },
    async submitReassignment(input) {
      const parsed = boundReassignmentSchema.safeParse(input);
      if (!parsed.success) throw new FleetError("configuration");
      return (await request(reassignMutation, { input: parsed.data }, z.object({ submitReassignment: mutationSchema }))).submitReassignment;
    },
  };
}
