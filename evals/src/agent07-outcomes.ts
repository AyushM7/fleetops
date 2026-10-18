import type { Trial } from "./agent07-runner.js";

export interface Branch {
  scenarioId: string; policy: string; trial: number; initialSimulatorStateSHA256: string;
  goValidation: string; serverVerifiedDecision: boolean | null; successfulReassignments: number; interventionFailures: number;
  decision: Trial | null;
  simulatorOutcome: { completedDeliveries: number; completionTick: number | null; unresolvedJobs: number; blockedRobotSamples: number; aggregateBatteryPercentagePoints: number; startTick: number; endTick: number } | null;
}
export function outcomeSummary(rows: Branch[]) {
  const policies = ["first-eligible", "minimum-pickup", "maximum-headroom", "v2-investigation"];
  const admission = policies.map(policy => { const all = rows.filter(r => r.policy === policy), attempts = all.reduce((n,r) => n+(r.decision?.submissionAttempts ?? 0),0);
    return { policy, admitted: { numerator: all.filter(r => r.goValidation === "pending_admitted").length, denominator: attempts }, unknownAdmissionAttemptReports: all.filter(r => r.decision && r.decision.submissionAttempts === null).length,
      successfulReassignments: all.reduce((n,r) => n+r.successfulReassignments,0), interventionFailures: all.reduce((n,r) => n+r.interventionFailures,0) }; });
  const comparisons = rows.filter(r => r.policy === "v2-investigation").map(treatment => {
    const same = rows.filter(r => r.scenarioId === treatment.scenarioId && r.trial === treatment.trial), control = same.find(r => r.policy === "no-intervention");
    const comparable = !!control?.simulatorOutcome && !!treatment.simulatorOutcome && same.every(r => r.initialSimulatorStateSHA256 === treatment.initialSimulatorStateSHA256 && r.simulatorOutcome?.startTick === control.simulatorOutcome!.startTick && r.simulatorOutcome?.endTick === control.simulatorOutcome!.endTick);
    const eligible = same.some(r => r.goValidation === "pending_admitted");
    const effects = comparable && eligible ? same.filter(r => r !== treatment && r.simulatorOutcome).map(baseline => ({ against: baseline.policy,
      completedDeliveryDelta: treatment.simulatorOutcome!.completedDeliveries-baseline.simulatorOutcome!.completedDeliveries,
      completionTicksSaved: treatment.simulatorOutcome!.completionTick === null || baseline.simulatorOutcome!.completionTick === null ? null : baseline.simulatorOutcome!.completionTick-treatment.simulatorOutcome!.completionTick,
      blockedRobotSampleDelta: treatment.simulatorOutcome!.blockedRobotSamples-baseline.simulatorOutcome!.blockedRobotSamples,
      finalStoredBatteryPercentagePointDelta: treatment.simulatorOutcome!.aggregateBatteryPercentagePoints-baseline.simulatorOutcome!.aggregateBatteryPercentagePoints,
      unresolvedJobDelta: treatment.simulatorOutcome!.unresolvedJobs-baseline.simulatorOutcome!.unresolvedJobs })) : null;
    return { scenarioId: treatment.scenarioId, trial: treatment.trial, comparable, eligibleIntervention: eligible, effects,
      interpretation: !comparable ? "Not comparable; no outcome speedup claimed" : !eligible ? "No eligible intervention; identical policy opportunity, no manufactured speedup" :
        treatment.decision?.source==="live-model" ? "Exploratory live choice: versus no-intervention measures intervention effect; deterministic-policy comparisons are small-sample observations, not statistical superiority" :
        "Versus no-intervention measures intervention effect; versus deterministic policies measures this scripted policy choice, NOT demonstrated real-model superiority" };
  });
  return { admission, comparisons, units: "Ticks and post-tick robot samples; battery is stored percentage-points, not kWh; censored completion remains null" };
}
