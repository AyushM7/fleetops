import { useEffect, useRef, useState, type JSX } from "react";
import { decideProposal, fetchProposal, pollPending, ProposalError, type Decision, type DecisionResult, type InboxFeed, type InboxPolling, type Proposal } from "./proposals";

export interface Receipt {
  id: string; decision: Decision; result: DecisionResult | null;
  latest: Proposal | null; refreshing: boolean; refreshFailed: boolean;
}
interface InboxViewProps {
  feed: InboxFeed; busy: string | null; receipt: Receipt | null; runtimeAvailable: boolean;
  decide: (proposal: Proposal, decision: Decision) => void; refresh: () => void; refreshStatus: () => void;
}

function DecisionReceipt({ receipt, refreshStatus, busy }: { receipt: Receipt; refreshStatus: () => void; busy: boolean }): JSX.Element {
  const result = receipt.result;
  return <div className="decision-receipt" role="status" aria-label="Decision result">
    <div className="receipt-heading"><h3>{!result || !result.auditCommitted ? "Decision outcome unknown" : result.error ? "Decision declined" : "Decision recorded"}</h3><code>{receipt.id}</code></div>
    {result?.error && <p className="decision-error">{result.error}</p>}
    {(!result || !result.auditCommitted) && <p className="decision-error">A committed audit is not confirmed. Do not assume execution failed or retry automatically. Refresh the status and reconcile the outcome.</p>}
    {result && <dl className="receipt-values"><div><dt>Response state</dt><dd>{result.state || "Not returned"}</dd></div><div><dt>Execution outcome</dt><dd>{result.execution}</dd></div><div><dt>Audit committed</dt><dd>{result.auditCommitted ? "Yes" : "Not confirmed"}</dd></div><div><dt>Execution attempted this call</dt><dd>{result.executionAttempted ? "Yes" : "No"}</dd></div></dl>}
    {receipt.refreshing ? <p>Refreshing proposal status…</p> : receipt.latest ? <p>Latest stored status: <strong>{receipt.latest.state}</strong> · execution <strong>{receipt.latest.execution}</strong>{!receipt.latest.currentRuntime && " · Prior runtime; cannot execute"}</p> : <p>{receipt.refreshFailed ? "Could not refresh the stored status. It may be stale." : "Proposal not found in persisted records."}</p>}
    {result?.error === "request denied" && <p>Go declined the request. The proposal may be stale; review the refreshed status and current fleet state.</p>}
    <button className="secondary-button" disabled={busy || receipt.refreshing} onClick={refreshStatus}>Refresh stored status</button>
    {result && <details><summary>Server response</summary><pre>{JSON.stringify(result, null, 2)}</pre></details>}
  </div>;
}

export function InboxView({ feed, busy, receipt, runtimeAvailable, decide, refresh, refreshStatus }: InboxViewProps): JSX.Element {
  const allowed = feed.phase === "ready" && runtimeAvailable && busy === null;
  return <section id="approval-inbox" className="approval-inbox" aria-label="Operator approval inbox">
    <div className="panel-heading"><div><h2>Approval inbox</h2><p>Current runtime · up to 50 pending proposals, oldest first</p></div><button className="secondary-button" onClick={refresh} disabled={busy !== null}>Refresh inbox</button></div>
    {feed.phase === "loading" && <p className="inbox-message" role="status">Loading pending proposals…</p>}
    {feed.phase === "forbidden" && <p className="inbox-message">Operator access required. Viewer and proposer credentials cannot review this inbox or approve decisions. Fleet monitoring remains available.</p>}
    {feed.phase === "unavailable" && <p className="inbox-message decision-error" role="alert">Approval inbox unavailable. Retrying… Displayed proposals may be stale; decisions are disabled.</p>}
    {feed.phase === "ready" && feed.proposals.length === 0 && <p className="inbox-message">No pending proposals in this runtime.</p>}
    {feed.phase !== "forbidden" && feed.proposals.length > 0 && <ul className="proposal-list">{feed.proposals.map(p => <li className="proposal-card" key={p.id} data-proposal-id={p.id}>
      <div className="proposal-summary"><div><h3>{p.robotId} <span>→ {p.stationId}</span></h3><p>Requested action: <strong>SetCharging</strong></p></div><span className="status">{p.state}</span></div>
      <code>{p.id}</code><p className="rationale">No rationale recorded</p>
      <p className="proposal-meta">Submitted by {p.submittedBy || "Not recorded"} · Expires {new Date(p.expiresAt).toLocaleString()}</p>
      {Date.parse(p.expiresAt) <= Date.now() && <p className="decision-error">Expiration time has passed. Go will recheck expiration before deciding.</p>}
      <div className="proposal-actions"><button className="approve-button" disabled={!allowed || !p.currentRuntime || p.state !== "pending"} onClick={() => decide(p, "approve")}>{busy === p.id ? "Processing…" : "Approve"}</button><button className="reject-button" disabled={!allowed || !p.currentRuntime || p.state !== "pending"} onClick={() => decide(p, "reject")}>Reject</button><span>Go revalidates before execution</span></div>
    </li>)}</ul>}
    {feed.proposals.length === 50 && <p className="inbox-message">The 50-result limit has been reached. Resolve proposals and refresh to see additional pending requests.</p>}
    {receipt && <DecisionReceipt receipt={receipt} refreshStatus={refreshStatus} busy={busy !== null} />}
  </section>;
}

export default function ApprovalInbox({ token, runtimeAvailable, onUnauthorized, onDecision }: { token: string; runtimeAvailable: boolean; onUnauthorized: () => void; onDecision: () => void }): JSX.Element {
  const [feed, setFeed] = useState<InboxFeed>({ phase: "loading", proposals: [], updatedAt: null });
  const [busy, setBusy] = useState<string | null>(null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const poller = useRef<InboxPolling | null>(null);
  const active = useRef<AbortController | null>(null);
  const inFlight = useRef(false);
  useEffect(() => {
    const polling = pollPending(token, setFeed, onUnauthorized); poller.current = polling;
    return () => { polling.stop(); active.current?.abort(); poller.current = null; };
  }, [token, onUnauthorized]);
  const readStatus = async (id: string, controller: AbortController) => {
    try {
      const latest = await fetchProposal(token, id, controller.signal);
      if (!controller.signal.aborted) setReceipt(previous => previous?.id === id ? { ...previous, latest, refreshing: false, refreshFailed: false } : previous);
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof ProposalError && error.kind === "authentication") { onUnauthorized(); return; }
      setReceipt(previous => previous?.id === id ? { ...previous, refreshing: false, refreshFailed: true } : previous);
    }
  };
  const decide = async (proposal: Proposal, decision: Decision) => {
    if (inFlight.current || feed.phase !== "ready" || !runtimeAvailable) return;
    inFlight.current = true; setBusy(proposal.id);
    active.current?.abort(); const controller = new AbortController(); active.current = controller;
    let result: DecisionResult | null = null;
    let authenticationLost = false;
    try { result = await decideProposal(token, proposal.id, decision, controller.signal); }
    catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof ProposalError && error.kind === "authentication") { authenticationLost = true; onUnauthorized(); return; }
    } finally {
      if (!controller.signal.aborted && !authenticationLost) {
        setReceipt({ id: proposal.id, decision, result, latest: null, refreshing: true, refreshFailed: false });
        if (result) poller.current?.settled(result);
        onDecision(); poller.current?.refresh();
        await readStatus(proposal.id, controller);
        if (!controller.signal.aborted) { inFlight.current = false; setBusy(null); }
      }
    }
  };
  const refreshStatus = async () => {
    if (!receipt || inFlight.current) return;
    inFlight.current = true; setBusy(receipt.id);
    active.current?.abort(); const controller = new AbortController(); active.current = controller;
    setReceipt(previous => previous ? { ...previous, refreshing: true } : previous);
    await readStatus(receipt.id, controller);
    if (!controller.signal.aborted) { setBusy(null); inFlight.current = false; }
    poller.current?.refresh(); onDecision();
  };
  return <InboxView feed={feed} busy={busy} receipt={receipt} runtimeAvailable={runtimeAvailable} decide={(proposal, decision) => { void decide(proposal, decision); }} refresh={() => poller.current?.refresh()} refreshStatus={() => { void refreshStatus(); }} />;
}
