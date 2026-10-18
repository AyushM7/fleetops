import type { JSX } from "react";
import type { DemoStatus, SimulationSpeed, TimeOperation } from "./restart";
import GuidedExperience from "./GuidedExperience";

export default function SimulationControls({ status, busy, unavailable, error, onControl }: {
  status: DemoStatus | null; busy: boolean; unavailable: boolean; error: string | null; onControl: (operation: TimeOperation) => void;
}): JSX.Element {
  const state = unavailable ? "unavailable" : status?.state ?? "unavailable";
  const running = state === "running";
  const blocked = unavailable || !status?.ready || !status.state || state === "unavailable";
  const awaiting = state === "awaiting_decision" || state === "preparing";
  const reason = blocked ? "Simulation unavailable. Refresh session status before another action." : awaiting
    ? "Awaiting the human decision. Approve or reject the proposal before advancing time."
    : "Approval transfers ownership. Delivery requires subsequent simulator ticks.";
  return <>{status?.guide && <GuidedExperience status={status} unavailable={unavailable} />}<div className="simulation-controls" aria-label="Simulation controls" aria-busy={busy}>
    <span className="simulation-state" role="status">{state === "preparing" ? "Preparing recommendation · Paused" : state === "awaiting_decision" ? "Awaiting Decision" : state === "running" ? "Running" : state === "paused" ? "Paused" : "Unavailable"}</span>
    <button className="primary-button" disabled={busy || blocked || awaiting} onClick={() => onControl(running ? "pause" : "play")} aria-describedby="simulation-help">{running ? "Pause" : "Play"}</button>
    <button className="secondary-button" disabled={busy || blocked || awaiting || running} onClick={() => onControl("step")} aria-describedby="simulation-help">Step Forward</button>
    <label>Speed <select aria-label="Simulation speed" value={status?.speed ?? "1"} disabled={busy || blocked} onChange={event => onControl(`speed/${event.target.value as SimulationSpeed}`)}><option value="0.5">0.5×</option><option value="1">1×</option><option value="2">2×</option></select></label>
    <span id="simulation-help" className="simulation-help">{busy ? "Applying control…" : state === "preparing" ? "Ticks are blocked while the trusted proposer prepares one pending proposal." : reason}</span>
    {error && <p role="alert" className="decision-error">{error}</p>}
  </div></>;
}
