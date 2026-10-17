import { useCallback, useEffect, useState, type FormEvent, type JSX } from "react";
import { pollFleet, type Feed, type FleetSnapshot, type Robot } from "./fleet";
import ApprovalInbox from "./ApprovalInbox";
import { Operations } from "./Operations";
import "./styles.css";
import { isLocalDemo } from "./demo";

const emptyFeed: Feed = { phase: "loading", snapshot: null, updatedAt: null };
const label = (status: string) => status.charAt(0).toUpperCase() + status.slice(1);

export function Warehouse({ fleet }: { fleet: FleetSnapshot }): JSX.Element {
  const { layout, robots, stations } = fleet;
  const size = 40;
  return <div className="map-scroll"><svg className="warehouse" viewBox={`-24 -25 ${layout.width * size + 30} ${layout.height * size + 31}`} role="img" aria-label={`Warehouse: ${layout.width} columns by ${layout.height} rows, ${robots.length} robots`}>
    {Array.from({ length: layout.width }, (_, x) => <text key={`x${x}`} x={x * size + 20} y={-10} className="coordinate" textAnchor="middle">{x}</text>)}
    {Array.from({ length: layout.height }, (_, y) => <text key={`y${y}`} x={-12} y={y * size + 24} className="coordinate" textAnchor="middle">{y}</text>)}
    {layout.cells.map(cell => <rect key={`${cell.x},${cell.y}`} x={cell.x * size} y={cell.y * size} width={size} height={size} className={`cell cell-${cell.kind}`} />)}
    {stations.map(s => <g key={s.id}><text x={s.x * size + 20} y={s.y * size + 36} textAnchor="middle" className="station-label">{s.id}</text><title>{`${s.id}: ${s.occupiedBy ? `occupied by ${s.occupiedBy}` : s.reservedBy ? `reserved for ${s.reservedBy}` : "available"}`}</title></g>)}
    {robots.map(r => <g key={r.id} transform={`translate(${r.x * size + 20},${r.y * size + 18})`} className={`robot robot-${r.status} ${r.blocked ? "robot-blocked" : ""}`}>
      <title>{`${r.id}: ${r.status}${r.blocked ? ", traffic blocked" : ""}, battery ${r.battery}%, job ${r.jobId || "none"}, position (${r.x},${r.y})`}</title>
      <rect x={-14} y={-12} width={28} height={25} rx={7} />
      <text textAnchor="middle" y={4}>{r.id}</text>
      {r.jobId && <text textAnchor="middle" y={22} className="robot-job">{r.jobId}</text>}
      {r.blocked && <text x={15} y={-12} className="blocked-mark">!</text>}
    </g>)}
  </svg></div>;
}

function RobotCard({ robot: r }: { robot: Robot }): JSX.Element {
  return <li className="robot-card" data-robot-id={r.id}>
    <div className="robot-heading"><strong><span className={`dot dot-${r.status}`} />{r.id}</strong><span className={`status status-${r.status}`}>{label(r.status)}</span></div>
    <div className="robot-meta"><span>Job <b>{r.jobId || "—"}</b></span><span>({r.x}, {r.y})</span></div>
    <div className="battery-line"><span>Battery</span><strong className={r.battery < 30 ? "low-battery" : ""}>{r.battery}%</strong></div>
    <meter className={r.battery < 30 ? "battery-low" : ""} min={0} max={100} value={r.battery} aria-label={`${r.id} battery ${r.battery}%`} />
    {r.blocked && <p className="blocked-note">Traffic blocked · waiting {r.waitTicks} ticks</p>}
    {(r.status === "faulted" || r.status === "offline") && <p className="fault-note">{r.status === "offline" ? "Robot unresponsive" : "Robot fault reported"}</p>}
  </li>;
}

export function Dashboard({ feed }: { feed: Feed }): JSX.Element {
  const fleet = feed.snapshot;
  return <>
    <div className="overview-heading"><div><p className="eyebrow">FLEET MONITOR</p><h1>Warehouse overview</h1><p className="subtitle">Robot positions, assignments, and fleet health.</p></div>
      <div className="tick"><span>Simulation tick</span><strong data-testid="tick">{fleet?.tick ?? "—"}</strong></div>
    </div>
    {feed.phase === "loading" && <div className="notice" role="status">Loading the warehouse…</div>}
    {feed.phase === "unavailable" && <div className="notice notice-error" role="alert">Backend unavailable. Retrying… {fleet ? "Showing the last received snapshot; positions may be stale." : "Waiting for fleet data."}</div>}
    {fleet?.poisoned && <div className="notice notice-error" role="alert">Runtime requires reconciliation. Simulator mutations have stopped.</div>}
    {fleet && <div className={`dashboard ${feed.phase === "unavailable" ? "stale" : ""}`}>
      <section className="warehouse-panel" aria-label="Warehouse map">
        <div className="panel-heading"><h2>Warehouse floor</h2><span>{fleet.layout.width} × {fleet.layout.height} cells</span></div>
        <Warehouse fleet={fleet} />
        <div className="legend"><span><i className="key key-aisle" />Aisle</span><span><i className="key key-station" />Station</span><span><i className="key key-wall" />Wall</span><span><i className="key key-moving" />Moving</span><span><i className="key key-blocked" />Blocked</span><span><i className="key key-offline" />Fault / offline</span></div>
        <p className="map-caption">Coordinates and layout come from the simulator. Updates every second.</p>
      </section>
      <aside className="fleet-panel" aria-label="Robot fleet"><div className="panel-heading"><h2>Robots</h2><span>{fleet.robots.length} in fleet</span></div><ul className="robot-list">{fleet.robots.map(r => <RobotCard key={r.id} robot={r} />)}</ul></aside>
    </div>}
    {fleet && <Operations fleet={fleet} stale={feed.phase !== "live"} />}
    <footer>{feed.updatedAt ? `Last received ${feed.updatedAt.toLocaleTimeString()}` : "Waiting for the first snapshot"}<span>Fleet monitoring</span></footer>
  </>;
}

export default function App(): JSX.Element {
  const [demo] = useState(isLocalDemo);
  const [token, setToken] = useState(() => isLocalDemo() ? "local-demo-session" : "");
  const [draft, setDraft] = useState("");
  const [authError, setAuthError] = useState(false);
  const [feed, setFeed] = useState<Feed>(emptyFeed);
  const [refresh, setRefresh] = useState(0);
  const onUnauthorized = useCallback(() => { setToken(""); setDraft(""); setFeed(emptyFeed); setAuthError(true); }, []);
  const onDecision = useCallback(() => setRefresh(value => value + 1), []);
  useEffect(() => {
    if (!token) return;
    return pollFleet(token, next => setFeed(previous => next.snapshot === null && previous.snapshot !== null ? { ...next, snapshot: previous.snapshot, updatedAt: previous.updatedAt } : next), onUnauthorized);
  }, [token, refresh, onUnauthorized]);
  const connect = (event: FormEvent) => { event.preventDefault(); if (!draft.trim()) return; setFeed(emptyFeed); setAuthError(false); setToken(draft.trim()); setDraft(""); };
  const disconnect = () => { setToken(""); setDraft(""); setFeed(emptyFeed); setAuthError(false); };
  return <div className="app-shell">
    <header className="topbar"><a className="brand" href="/" aria-label="FleetOps home"><span className="brand-mark">F</span>FleetOps<span className="brand-divider" /> <span className="brand-context">Warehouse operations</span></a>
      {token && <div className="connection"><span className={`connection-badge ${feed.phase === "live" ? "connected" : ""}`}><i />{feed.phase === "live" ? "Connected" : feed.phase === "loading" ? "Connecting" : "Disconnected"}</span><a className="inbox-link" href="#approval-inbox">Approvals</a>{!demo && <button className="quiet-button" onClick={disconnect}>Disconnect</button>}</div>}
    </header>
    <main>{demo && <div className="notice" role="status">Demo Mode · Local simulation · Deterministic AI recommendation · Approval is always your choice.</div>}{demo && !token ? <div className="notice notice-error" role="alert">Demo session ended. Reload the local demo URL while the runner is active.</div> : !token ? <section className="login-panel"><p className="eyebrow">LIVE WAREHOUSE</p><h1>Your fleet, in view.</h1><p>Connect to the running simulator with your bearer token.</p>
      <form onSubmit={connect}><label htmlFor="viewer-token">Bearer token</label><input id="viewer-token" type="password" autoComplete="off" spellCheck={false} value={draft} onChange={event => setDraft(event.target.value)} placeholder="Paste a viewer or operator token" required /><button className="primary-button" type="submit">Connect to fleet <span aria-hidden="true">→</span></button></form>
      {authError && <p className="auth-error" role="alert">Authentication failed. Check your token and connect again.</p>}
      <p className="privacy-note">Viewer credentials monitor the fleet; operator credentials enable the approval inbox. Your token stays in memory for this tab and is cleared on disconnect or refresh.</p></section> : <><Dashboard feed={feed} /><ApprovalInbox token={token} runtimeAvailable={feed.phase === "live" && feed.snapshot?.poisoned === false} onUnauthorized={onUnauthorized} onDecision={onDecision} /></>}</main>
  </div>;
}
