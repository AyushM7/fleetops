import type { JSX } from "react";
import { parseSnapshot, type FleetSnapshot } from "./fleet";
import { isScenarioID, type ScenarioID } from "./scenarios";
import { parseEvidence, type ExplanationEvidence } from "./Evidence";

export type SimulationState = "awaiting_decision" | "paused" | "running" | "unavailable";
export type SimulationSpeed = "0.5" | "1" | "2";
export type TimeOperation = "play" | "pause" | "step" | `speed/${SimulationSpeed}`;
export interface DemoStatus { generation: string; ready: boolean; scenarioId?: ScenarioID; state?: SimulationState; speed?: SimulationSpeed; snapshot?: FleetSnapshot; evidence?: ExplanationEvidence }
export class DemoError extends Error {
  constructor(public readonly authentication: boolean, message: string) { super(message); }
}

async function demoRequest(path: "status" | "restart" | `scenario/${ScenarioID}` | TimeOperation, generation: string, signal: AbortSignal, tick?: number): Promise<DemoStatus> {
  const response = await fetch(`/demo/${path}`, {
    method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error", signal,
    headers: { "Content-Type": "application/json", "X-FleetOps-Demo": "1", "X-FleetOps-Generation": generation, ...(tick === undefined ? {} : { "X-FleetOps-Tick": String(tick) }) }, body: "{}",
  });
  if (response.status === 401 || response.status === 403) throw new DemoError(true, "Demo session ended. Reload the local demo URL.");
  if (!response.ok) throw new DemoError(false, path === "restart" || path.startsWith("scenario/")
    ? "Restart was not confirmed. The previous run may have stopped. Refresh session status before retrying; historical outcomes may require reconciliation."
    : path === "status" ? "Demo session status unavailable. Decisions are disabled until status is refreshed."
    : "Simulation control was not confirmed. Refresh status before another action; do not retry an unknown Step outcome.");
  const body: unknown = await response.json();
  if (typeof body !== "object" || body === null || !("generation" in body) || typeof body.generation !== "string" || !/^[a-f0-9]{64}$/.test(body.generation) || !("ready" in body) || typeof body.ready !== "boolean") {
    throw new DemoError(false, "Invalid demo status. Restart success cannot be confirmed.");
  }
  const status: DemoStatus = { generation: body.generation, ready: body.ready };
  if ("scenarioId" in body) {
    if (!isScenarioID(body.scenarioId)) throw new DemoError(false, "Invalid scenario identity.");
    status.scenarioId = body.scenarioId;
  }
  if ("state" in body || "speed" in body || "snapshot" in body) {
    if (!("state" in body) || !["awaiting_decision", "paused", "running", "unavailable"].includes(body.state as string) ||
        !("speed" in body) || !["0.5", "1", "2"].includes(body.speed as string)) throw new DemoError(false, "Invalid simulation status.");
    status.state = body.state as SimulationState; status.speed = body.speed as SimulationSpeed;
    if ("snapshot" in body) status.snapshot = parseSnapshot(body.snapshot);
    if (status.state !== "unavailable" && (!status.ready || !status.snapshot || status.snapshot.poisoned)) throw new DemoError(false, "Invalid simulation readiness.");
  }
  if ("evidence" in body) status.evidence = parseEvidence(body.evidence, status.generation, status.scenarioId);
  return status;
}

export async function controlSimulation(operation: TimeOperation, generation: string, tick: number): Promise<DemoStatus> {
  if (!["play", "pause", "step", "speed/0.5", "speed/1", "speed/2"].includes(operation)) throw new DemoError(false, "Unsupported simulation control.");
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const status = await demoRequest(operation, generation, controller.signal, tick);
    if (status.generation !== generation || !status.state) throw new DemoError(false, "Simulation changed; refresh status before another action.");
    return status;
  } finally { clearTimeout(timeout); }
}

export async function restartScenario(generation: string): Promise<DemoStatus> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 65000);
  try {
    const status = await demoRequest("restart", generation, controller.signal);
    if (!status.ready || status.generation === generation) throw new DemoError(false, "Fresh scenario readiness was not confirmed. Refresh session status.");
    return status;
  } finally { clearTimeout(timeout); }
}

export async function selectScenario(id: ScenarioID, generation: string): Promise<DemoStatus> {
  if (!isScenarioID(id)) throw new DemoError(false, "Unsupported scenario.");
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 65000);
  try {
    const status = await demoRequest(`scenario/${id}`, generation, controller.signal);
    if (!status.ready || status.generation === generation || status.scenarioId !== id) throw new DemoError(false, "Selected scenario readiness was not confirmed. Refresh session status.");
    return status;
  } finally { clearTimeout(timeout); }
}

export function pollDemoStatus(emit: (status: DemoStatus | null, error: string | null) => void, unauthorized: () => void, interval = 1000): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let active: AbortController | undefined;
  const poll = async () => {
    active = new AbortController();
    const timeout = setTimeout(() => active?.abort(), 8000);
    try {
      const status = await demoRequest("status", "", active.signal);
      if (!stopped) emit(status, status.ready ? null : "Scenario stopped. Restart or reconcile the historical outcome; decisions are disabled.");
    } catch (error) {
      if (stopped) return;
      if (error instanceof DemoError && error.authentication) { unauthorized(); return; }
      emit(null, error instanceof DemoError ? error.message : "Demo session status unavailable. Decisions are disabled.");
    } finally { clearTimeout(timeout); }
    if (!stopped) timer = setTimeout(() => { void poll(); }, interval);
  };
  void poll();
  return () => { stopped = true; clearTimeout(timer); active?.abort(); };
}

export function RestartControl({ busy, disabled, error, onRestart }: { busy: boolean; disabled: boolean; error: string | null; onRestart: () => void }): JSX.Element {
  return <div className="scenario-control" aria-label="Scenario controls">
    <button className="secondary-button" type="button" disabled={disabled || busy} onClick={onRestart}>{busy ? "Restarting scenario…" : "Restart Scenario"}</button>
    {busy && <p role="status" aria-live="polite">Stopping the previous run, capturing evidence, and preparing a fresh scenario…</p>}
    <details className="restart-info"><summary>Restart info</summary><p>Restarts the selected scenario · Prior evidence retained. Uncertain outcomes still require reconciliation.</p></details>
    {error && <p className="decision-error" role="alert">{error}</p>}
  </div>;
}
