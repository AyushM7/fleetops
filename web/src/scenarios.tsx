import type { JSX } from "react";

export const scenarios = [
  { id: "blocked-reassignment", name: "Blocked job recovery", description: "Starts with blocked R1 holding J1; review a Go-admitted transfer to R3, then advance time to deliver." },
  { id: "routine-charging", name: "Routine charging", description: "Starts with R1 at S1 and 50% battery after 1,000 idle ticks; approve charging in place, then advance time." },
  { id: "healthy-operations", name: "Healthy fleet operations", description: "A shelf warehouse starts with six healthy robots and eight jobs; play to observe autonomous operations. No recovery proposal is needed at startup." },
  { id: "blocked-no-candidate", name: "No eligible recovery", description: "Starts with R1 blocked, R2 offline, and all idle recipients below 30%. No supported transfer at startup. Operator attention required; observe or switch scenarios." },
] as const;
export type ScenarioID = typeof scenarios[number]["id"];
export function isScenarioID(value: unknown): value is ScenarioID { return scenarios.some(scenario => scenario.id === value); }

export function ScenarioSelector({ selected, disabled, busy, onSelect }: { selected?: ScenarioID; disabled: boolean; busy: boolean; onSelect: (id: ScenarioID) => void }): JSX.Element {
  const scenario = scenarios.find(item => item.id === selected);
  return <div className="scenario-picker" aria-busy={busy}>
    <label>Scenario <select aria-label="Warehouse scenario" aria-describedby="scenario-description" value={selected ?? ""} disabled={disabled || busy} onChange={event => { if (isScenarioID(event.target.value)) onSelect(event.target.value); }}>
      {!selected && <option value="">Loading scenarios…</option>}
      {scenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
    </select></label>
    <p id="scenario-description" role="status">{busy ? "Stopping the previous run, preserving evidence, and preparing the selected scenario…" : scenario?.description ?? "Waiting for the trusted scenario status."}</p>
  </div>;
}
