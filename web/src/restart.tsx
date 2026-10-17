import type { JSX } from "react";

export interface DemoStatus { generation: string; ready: boolean }
export class DemoError extends Error {
  constructor(public readonly authentication: boolean, message: string) { super(message); }
}

async function demoRequest(path: "status" | "restart", generation: string, signal: AbortSignal): Promise<DemoStatus> {
  const response = await fetch(`/demo/${path}`, {
    method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error", signal,
    headers: { "Content-Type": "application/json", "X-FleetOps-Demo": "1", "X-FleetOps-Generation": generation }, body: "{}",
  });
  if (response.status === 401 || response.status === 403) throw new DemoError(true, "Demo session ended. Reload the local demo URL.");
  if (!response.ok) throw new DemoError(false, path === "restart"
    ? "Restart was not confirmed. The previous run may have stopped. Refresh session status before retrying; historical outcomes may require reconciliation."
    : "Demo session status unavailable. Decisions are disabled until status is refreshed.");
  const body: unknown = await response.json();
  if (typeof body !== "object" || body === null || !("generation" in body) || typeof body.generation !== "string" || !/^[a-f0-9]{64}$/.test(body.generation) || !("ready" in body) || typeof body.ready !== "boolean") {
    throw new DemoError(false, "Invalid demo status. Restart success cannot be confirmed.");
  }
  return { generation: body.generation, ready: body.ready };
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

export function pollDemoStatus(emit: (status: DemoStatus | null, error: string | null) => void, unauthorized: () => void): () => void {
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
    if (!stopped) timer = setTimeout(() => { void poll(); }, 1000);
  };
  void poll();
  return () => { stopped = true; clearTimeout(timer); active?.abort(); };
}

export function RestartControl({ busy, disabled, error, onRestart }: { busy: boolean; disabled: boolean; error: string | null; onRestart: () => void }): JSX.Element {
  return <div className="scenario-control" aria-label="Scenario controls">
    <button className="secondary-button" type="button" disabled={disabled || busy} onClick={onRestart}>{busy ? "Restarting scenario…" : "Restart Scenario"}</button>
    {busy && <p role="status" aria-live="polite">Stopping the previous run, capturing evidence, and preparing a fresh pending proposal…</p>}
    <details className="restart-info"><summary>Restart info</summary><p>Fresh tick-42 run · Prior evidence retained. Uncertain outcomes still require reconciliation.</p></details>
    {error && <p className="decision-error" role="alert">{error}</p>}
  </div>;
}
