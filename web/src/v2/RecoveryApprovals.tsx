import { useEffect, useRef, useState } from "react";
import { postVisitor } from "../demo";
import type { Snapshot } from "./telemetry";

type RecoveryState = "pending" | "approved" | "rejected" | "expired";
type RecoveryItem = {
  runId: string;
  state: RecoveryState;
  execution: "none" | "succeeded" | "failed" | "unknown";
  selection: { jobId: string; fromRobotId: string; toRobotId: string };
  basis: string;
  metrics: Record<string, number> | null;
  incidents: { robotId: string; type: string; severity: string }[];
};

const states = new Set(["pending", "approved", "rejected", "expired"]);
const executions = new Set(["none", "succeeded", "failed", "unknown"]);
const basisLabels: Record<string, string> = {
  pickup_cost: "Shortest eligible pickup",
  battery_headroom: "Most available battery headroom",
  admitted_option: "Selected from Go-admitted options",
  tradeoff: "Compared pickup and battery tradeoffs",
};

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid recovery record");
  return value as Record<string, unknown>;
}

export function parseRecoveryItems(value: unknown): RecoveryItem[] {
  if (!Array.isArray(value) || value.length > 20) throw new Error("Invalid recovery history");
  return value.map(item => {
    const row = record(item);
    const key = record(row.selection);
    if (typeof row.runId !== "string" || !/^A[a-f0-9]{32}$/.test(row.runId) ||
        typeof row.state !== "string" || !states.has(row.state) ||
        typeof row.execution !== "string" || !executions.has(row.execution) ||
        typeof row.basis !== "string" ||
        typeof key.jobId !== "string" || !/^J\d+$/.test(key.jobId) ||
        typeof key.fromRobotId !== "string" || !/^R(?:10|[1-9])$/.test(key.fromRobotId) ||
        typeof key.toRobotId !== "string" || !/^R(?:10|[1-9])$/.test(key.toRobotId) ||
        !Array.isArray(row.incidents) || row.incidents.length > 64) throw new Error("Invalid recovery proposal");
    const metrics = row.metrics === null ? null : record(row.metrics);
    if (metrics && Object.values(metrics).some(n => !Number.isSafeInteger(n))) throw new Error("Invalid Go evidence");
    const incidents = row.incidents.map(value => {
      const incident = record(value);
      if (typeof incident.robotId !== "string" || typeof incident.type !== "string" || typeof incident.severity !== "string") {
        throw new Error("Invalid incident");
      }
      return { robotId: incident.robotId, type: incident.type, severity: incident.severity };
    });
    return {
      runId: row.runId, state: row.state as RecoveryState, execution: row.execution as RecoveryItem["execution"],
      selection: { jobId: key.jobId, fromRobotId: key.fromRobotId, toRobotId: key.toRobotId },
      basis: row.basis, metrics: metrics as Record<string, number> | null, incidents,
    };
  });
}

type Decision = "approve" | "reject";

export function RecoveryProposalList({ items, blocked, pending, submitted, snapshot, decide }: {
  items: RecoveryItem[]; blocked: boolean; pending: string | null;
  submitted?: ReadonlySet<string>; snapshot?: Pick<Snapshot, "jobs" | "robots" | "tick">;
  decide: (runId: string, action: Decision) => void;
}) {
  if (!items.length) return <p className="v2-ops-empty">No verified recovery proposals for this run.</p>;
  return <ul className="v2-recovery-list">{items.map(item => {
    const { jobId, fromRobotId, toRobotId } = item.selection;
    const job = item.execution === "succeeded" ? snapshot?.jobs.find(job => job.id === jobId) : undefined;
    const robot = job && snapshot?.robots.find(robot => robot.id === job.assignedRobot);
    const decisionBlocked = blocked || pending !== null || submitted?.has(item.runId);
    const state = item.execution === "succeeded" ? "Approved and executed" :
      item.execution === "failed" ? "Approved, execution failed" :
        item.execution === "unknown" ? "Execution unconfirmed" : item.state[0].toUpperCase() + item.state.slice(1);
    return <li key={item.runId}>
      <strong>Reassign {jobId} from {fromRobotId} to {toRobotId}</strong>
      <p role="status">{state}</p>
      {job && <p>Tick {snapshot?.tick} · {job.status.replaceAll("_", " ")} · Owner {job.assignedRobot || "unassigned"}{robot && <> · {robot.status} · {robot.battery}% battery</>}</p>}
      {item.incidents.length > 0 && <p>{item.incidents.map(incident => incident.type.replaceAll("_", " ")).join(", ")}</p>}
      <p>{Object.hasOwn(basisLabels, item.basis) ? basisLabels[item.basis] : "Verified Go proposal"}</p>
      {item.metrics && <p>Pickup {item.metrics.pickupSteps ?? "unknown"} steps · Required battery {item.metrics.requiredBattery ?? "unknown"}%</p>}
      {item.state === "pending" && <div className="v2-recovery-actions">
        <button type="button" disabled={decisionBlocked} onClick={() => decide(item.runId, "approve")}>Approve {jobId}</button>
        <button type="button" disabled={decisionBlocked} onClick={() => decide(item.runId, "reject")}>Reject {jobId}</button>
      </div>}
    </li>;
  })}</ul>;
}

export function RecoveryApprovals({ generation, live, snapshot }: { generation: string; live: boolean; snapshot: Snapshot }) {
  const [items, setItems] = useState<RecoveryItem[]>([]);
  const [pending, setPending] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  const [ready, setReady] = useState(false);
  const decisionRef = useRef(false);
  const uncertainRef = useRef(new Set<string>());
  const lifetime = useRef<AbortController | null>(null);

  useEffect(() => {
    const scope = new AbortController();
    lifetime.current = scope;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const result = await postVisitor("/demo/recovery", generation, AbortSignal.any([scope.signal, AbortSignal.timeout(8000)]));
        if (!result.ok) throw new Error("Recovery status unavailable");
        const rows = parseRecoveryItems(await result.json());
        if (scope.signal.aborted) return;
        setItems(rows);
        setReady(true);
      } catch {
        if (!scope.signal.aborted) setReady(false);
      } finally {
        if (!scope.signal.aborted) timer = setTimeout(() => { void refresh(); }, 3000);
      }
    }
    void refresh();
    return () => { scope.abort(); clearTimeout(timer); lifetime.current = null; };
  }, [generation]);

  async function decide(runId: string, action: Decision) {
    const scope = lifetime.current;
    if (!scope || !live || !ready || decisionRef.current || uncertainRef.current.has(runId) || !items.some(row => row.runId === runId && row.state === "pending")) return;
    decisionRef.current = true;
    uncertainRef.current.add(runId);
    setPending(runId);
    setNotice("");
    try {
      const result = await postVisitor(`/demo/recovery/${runId}/${action}`, generation, AbortSignal.any([scope.signal, AbortSignal.timeout(12000)]));
      if (scope.signal.aborted) return;
      setNotice(result.ok ? "Decision recorded. Waiting for the updated Go receipt." :
        "Decision declined or stale. Check the updated Go record before taking another action.");
    } catch {
      if (!scope.signal.aborted) setNotice("Decision outcome unconfirmed. Check the refreshed Go record before taking another action.");
    } finally {
      decisionRef.current = false;
      if (!scope.signal.aborted) setPending(null);
    }
  }

  return <section className="v2-panel v2-ops-group" aria-labelledby="recovery-proposals-title">
    <header><h2 id="recovery-proposals-title">Recovery proposals</h2></header>
    {!ready && <p role="status">Waiting for current Go proposal records.</p>}
    {notice && <p role="status">{notice}</p>}
    {(ready || items.length > 0) && <RecoveryProposalList items={items} blocked={!live || !ready} pending={pending}
      submitted={uncertainRef.current} snapshot={snapshot} decide={(id, action) => { void decide(id, action); }} />}
  </section>;
}
