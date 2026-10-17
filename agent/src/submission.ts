import type { FleetAPI } from "./graphql.js";
import type { Analysis, FleetState, ProposalStatus, SubmissionCheck } from "./contracts.js";
import { sameFacts, validateSnapshot } from "./reasoning.js";
import { createFleetTools } from "./tools.js";

export async function submitReviewedAction(api: FleetAPI, snapshot: FleetState, action: Analysis["recommendedAction"], options: {
  allowSubmit?: boolean; demo?: { robotId: string; stationId: string }; now?: () => Date;
}): Promise<{ proposal: ProposalStatus; submissionCheck: SubmissionCheck }> {
  let submissionCheck: SubmissionCheck = options.allowSubmit ? "not_requested" : "disabled";
  const session = createFleetTools({
    async readFleet() {
      let current: FleetState;
      try { current = validateSnapshot(await api.readFleet()); }
      catch { submissionCheck = "unavailable"; throw new Error("Fresh snapshot unavailable."); }
      if (!sameFacts(current, snapshot)) {
        submissionCheck = "snapshot_changed";
        throw new Error("Snapshot changed; request a new analysis.");
      }
      submissionCheck = "current";
      return current;
    },
    submitCharging: input => api.submitCharging(input),
  }, options.allowSubmit === true, options.demo);
  if (action && options.allowSubmit) {
    await session.submit({ robotId: action.robotId, stationId: action.stationId,
      expiresAt: new Date((options.now?.() ?? new Date()).getTime() + 60_000).toISOString() });
  }
  return { proposal: session.status(), submissionCheck };
}
