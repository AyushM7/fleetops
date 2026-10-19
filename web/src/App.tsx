import { useCallback, useEffect, useRef, useState, type FormEvent, type JSX, type ReactNode } from "react";
import { pollFleet, type Feed } from "./fleet";
import ApprovalInbox from "./ApprovalInbox";
import { ActiveIncidents, JobProgress } from "./Operations";
import { RobotCard, Warehouse } from "./Warehouse";
import "./styles.css";
import { isLocalDemo } from "./demo";
import { controlSimulation, DemoError, pollDemoStatus, restartScenario, selectScenario, RestartControl, type DemoStatus, type TimeOperation } from "./restart";
import { ScenarioSelector, type ScenarioID } from "./scenarios";
import SimulationControls from "./SimulationControls";
import AgentActivity from "./AgentActivity";

export { Warehouse } from "./Warehouse";

const emptyFeed: Feed = { phase: "loading", snapshot: null, updatedAt: null };
export function Dashboard({ feed, decisionPanel, simulationControls, agentActivity }: { feed: Feed; decisionPanel?: ReactNode; simulationControls?: ReactNode; agentActivity?: ReactNode }): JSX.Element {
  const fleet = feed.snapshot;
  const [selection, setSelection] = useState<string | null>(null);
  const selectedRobot = fleet?.robots.find(r => r.id === selection) ?? null;
  const selectedRobotID = selectedRobot?.id ?? null;
  return <>
    {feed.phase === "loading" && <div className="notice" role="status">Loading the warehouse…</div>}
    {feed.phase === "unavailable" && <div className="notice notice-error" role="alert">Backend unavailable. Retrying… {fleet ? "Showing the last received snapshot; positions may be stale." : "Waiting for fleet data."}</div>}
    {fleet?.poisoned && <div className="notice notice-critical" role="alert">Runtime requires reconciliation. Simulator mutations have stopped.</div>}
    <div className={`dashboard ${feed.phase === "unavailable" ? "stale" : ""}`}>
      {fleet && <>
      <section id="warehouse" className="warehouse-panel" aria-label="Warehouse map">
        <div className="map-instruments"><div><h1>Warehouse</h1></div><span className="layout-dimensions">{fleet.layout.width} × {fleet.layout.height}</span><div className="tick"><span>Simulation tick</span><strong data-testid="tick">{fleet.tick}</strong></div></div>
        {simulationControls}
        <Warehouse fleet={fleet} selectedRobotID={selectedRobotID} onSelectRobot={setSelection} />
        <div className="map-tools"><div className="map-selection" role="status">{selectedRobot ? <><span><strong>{selectedRobot.id} selected</strong> · {selectedRobot.status} · ({selectedRobot.x}, {selectedRobot.y}){selectedRobot.blocked && " · Traffic blocked"} · Battery {selectedRobot.battery}% · Job {selectedRobot.jobId || "none"}</span><button className="text-button" onClick={() => setSelection(null)}>Clear robot selection</button></> : <span>Select a robot to inspect.</span>}</div>
        <details className="map-guide"><summary>Map key & coordinates</summary><div className="legend"><span><i className="key key-free" />Floor</span><span><i className="key key-aisle" />Aisle</span><span><i className="key key-station" />Station</span><span><i className="key key-wall" />Wall</span><span><i className="key key-moving" />Moving</span><span><i className="key key-blocked" />Blocked</span><span><i className="key key-offline" />Fault / offline</span><span><i className="key key-selected" />Selected</span></div>
        <p className="map-caption">Simulator coordinates · Authoritative sampled snapshots. Routes are not supplied.</p></details></div>
      </section>
      </>}
      <aside className="operator-context" aria-label="Operator context">
        {fleet && <ActiveIncidents fleet={fleet} stale={feed.phase !== "live"} selectedRobotID={selectedRobotID} onSelectRobot={setSelection} />}
        <div className="decision-panel">{decisionPanel}</div>
        {agentActivity}
        {fleet && <>
          <details className="fleet-panel"><summary>Robot fleet <span>{fleet.robots.length} robots</span></summary><ul className="robot-list">{fleet.robots.map(r => <RobotCard key={r.id} robot={r} selectedRobotID={selectedRobotID} onSelectRobot={setSelection} />)}</ul></details>
          <details className="job-disclosure"><summary>Job progress <span>{fleet.jobs.filter(j => j.status !== "done").length} open jobs</span></summary><JobProgress fleet={fleet} stale={feed.phase !== "live"} /></details>
        </>}
      </aside>
    </div>
    <footer>{feed.updatedAt ? `Last received ${feed.updatedAt.toLocaleTimeString()}` : "Waiting for the first snapshot"}<span>LLM proposes. Go validates. Human approves. Go executes.</span></footer>
  </>;
}

export default function App(): JSX.Element {
  const [demo] = useState(isLocalDemo);
  const [token, setToken] = useState(() => isLocalDemo() ? "local-demo-session" : "");
  const [draft, setDraft] = useState("");
  const [authError, setAuthError] = useState(false);
  const [feed, setFeed] = useState<Feed>(emptyFeed);
  const [refresh, setRefresh] = useState(0);
  const [generation, setGeneration] = useState("");
  const generationRef = useRef("");
  const [scenarioReady, setScenarioReady] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const restartInFlight = useRef(false);
  const [restartError, setRestartError] = useState<string | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [statusKnown, setStatusKnown] = useState(false);
  const [simulationStatus, setSimulationStatus] = useState<DemoStatus | null>(null);
  const [controlling, setControlling] = useState(false);
  const controlInFlight = useRef(false);
  const [controlError, setControlError] = useState<string | null>(null);
  const onUnauthorized = useCallback(() => { setToken(""); setDraft(""); setFeed(emptyFeed); setAuthError(true); }, []);
  const onDecision = useCallback(() => setRefresh(value => value + 1), []);
  const acceptStatus = useCallback((status: DemoStatus) => {
    if (generationRef.current !== status.generation) {
      generationRef.current = status.generation;
      setGeneration(status.generation); setFeed(emptyFeed); setRestartError(null);
      setControlError(null);
    }
    setScenarioReady(status.ready);
    setSimulationStatus(status);
    if (status.snapshot) setFeed({ phase: "live", snapshot: status.snapshot, updatedAt: new Date() });
  }, []);
  useEffect(() => {
    if (!demo || !token || restarting || controlling) return;
    return pollDemoStatus((status, error) => {
      if (restartInFlight.current || controlInFlight.current) return;
      setStatusError(error);
      setStatusKnown(status !== null);
      if (status) acceptStatus(status); else setScenarioReady(false);
      if (!status?.ready) setFeed(previous => previous.snapshot ? { ...previous, phase: "unavailable" } : previous);
    }, onUnauthorized, 250);
  }, [demo, token, restarting, controlling, refresh, acceptStatus, onUnauthorized]);
  useEffect(() => {
    if (!token || restarting || demo) return;
    return pollFleet(demo ? generation : token, next => setFeed(previous => next.snapshot === null && previous.snapshot !== null ? { ...next, snapshot: previous.snapshot, updatedAt: previous.updatedAt } : next), onUnauthorized);
  }, [token, refresh, onUnauthorized, demo, generation, scenarioReady, restarting]);
  const control = async (operation: TimeOperation) => {
    if (controlInFlight.current || restartInFlight.current || !statusKnown || !simulationStatus?.snapshot) return;
    const expectedGeneration = generation;
    controlInFlight.current = true; setControlling(true); setControlError(null); setStatusKnown(false);
    try {
      const status = await controlSimulation(operation, expectedGeneration, simulationStatus.snapshot.tick);
      if (!restartInFlight.current && generationRef.current === expectedGeneration) {
        acceptStatus(status); setStatusKnown(true); setStatusError(null);
      }
    } catch (error) {
      if (error instanceof DemoError && error.authentication) onUnauthorized();
      if (!restartInFlight.current && generationRef.current === expectedGeneration) setControlError(error instanceof DemoError ? error.message : "Control outcome unknown. Refresh status; do not retry Step automatically.");
    } finally { controlInFlight.current = false; setControlling(false); }
  };
  const restart = async (selected?: ScenarioID) => {
    if (restartInFlight.current || controlInFlight.current || !generation || !token || !statusKnown) return;
    restartInFlight.current = true; setRestarting(true); setRestartError(null); setFeed(emptyFeed); setScenarioReady(false); setStatusKnown(false);
    setSimulationStatus(null); setControlError(null);
    try { acceptStatus(await (selected ? selectScenario(selected, generation) : restartScenario(generation))); }
    catch (error) {
      if (error instanceof DemoError && error.authentication) onUnauthorized();
      setRestartError(error instanceof DemoError ? error.message : "Restart outcome unknown. Refresh session status before retrying. Historical outcomes may require reconciliation.");
    } finally { restartInFlight.current = false; setRestarting(false); }
  };
  const connect = (event: FormEvent) => { event.preventDefault(); if (!draft.trim()) return; setFeed(emptyFeed); setAuthError(false); setToken(draft.trim()); setDraft(""); };
  const disconnect = () => { setToken(""); setDraft(""); setFeed(emptyFeed); setAuthError(false); };
  return <div className="app-shell">
    <a className="skip-link" href="#main-content">Skip to warehouse</a><header className="topbar"><a className="brand" href="/" aria-label="FleetOps home"><span className="brand-mark">F</span>FleetOps<span className="brand-divider" /> <span className="brand-context">Fleet control</span></a>
      {token && <div className="connection">{demo && <span className="demo-badge">Visitor demo · No live LLM</span>}<span className={`connection-badge ${feed.phase === "live" ? "connected" : ""}`}><i />{feed.phase === "live" ? "Connected" : feed.phase === "loading" ? "Connecting" : "Disconnected"}</span><a className="inbox-link" href="#approval-inbox">Decisions</a>{!demo && <button className="quiet-button" onClick={disconnect}>Disconnect</button>}</div>}
      {demo && token && <RestartControl busy={restarting} disabled={!generation || !statusKnown || controlling} error={restartError || statusError} onRestart={() => { void restart(); }} />}
    </header>
    {demo && token && <ScenarioSelector selected={simulationStatus?.scenarioId} disabled={!generation || !statusKnown || controlling} busy={restarting} onSelect={id => { void restart(id); }} />}
    <main id="main-content" tabIndex={-1}>{demo && !token ? <div className="notice notice-error" role="alert">Visitor session ended. Clear this site's cookies to begin a new visit.</div> : !token ? <section className="login-panel"><p className="eyebrow">LIVE WAREHOUSE</p><h1>Your fleet, in view.</h1><p>Connect to the running simulator with your bearer token.</p>
      <form onSubmit={connect}><label htmlFor="viewer-token">Bearer token</label><input id="viewer-token" type="password" autoComplete="off" spellCheck={false} value={draft} onChange={event => setDraft(event.target.value)} placeholder="Paste a viewer or operator token" required /><button className="primary-button" type="submit">Connect to fleet <span aria-hidden="true">→</span></button></form>
      {authError && <p className="auth-error" role="alert">Authentication failed. Check your token and connect again.</p>}
      <p className="privacy-note">Viewer credentials monitor the fleet; operator credentials enable the approval inbox. Your token stays in memory for this tab and is cleared on disconnect or refresh.</p></section> : <><Dashboard key={demo ? generation : "normal"} feed={feed} agentActivity={<AgentActivity key={demo ? generation : token} token={demo ? generation : token} available={feed.phase === "live" && !restarting && (!demo || scenarioReady && statusKnown)} revision={refresh} onUnauthorized={onUnauthorized} />} simulationControls={demo ? <SimulationControls status={simulationStatus} busy={controlling || restarting} unavailable={!statusKnown || !scenarioReady || feed.phase !== "live" || feed.snapshot?.poisoned !== false} error={controlError} onControl={operation => { void control(operation); }} /> : undefined} decisionPanel={demo && (!generation || restarting) ? <section id="approval-inbox" className="approval-inbox"><h2>Approval inbox</h2><p role="status">Waiting for the fresh scenario…</p></section> : <ApprovalInbox key={demo ? generation : "normal"} token={demo ? generation : token} evidenceContext={demo ? { generation, scenarioId: simulationStatus?.scenarioId, evidence: simulationStatus?.evidence, current: feed.snapshot, available: statusKnown && scenarioReady && feed.phase === "live" } : undefined} runtimeAvailable={feed.phase === "live" && feed.snapshot?.poisoned === false && (!demo || scenarioReady)} onUnauthorized={onUnauthorized} onDecision={onDecision} />} /></>}</main>
  </div>;
}
