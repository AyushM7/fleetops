import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, type JSX } from "react";
import { DemoError, selectScenario } from "../restart";
import type { ScenarioID } from "../scenarios";
import { allowsWarehouseAction, subscribeWarehouse, type WarehouseAction, type WarehouseFeed } from "./session";
import { EntityDirectory } from "./EntityDirectory";
import { Warehouse } from "./Warehouse";
import { Icon } from "./WarehouseIcon";
import { findEntity, type Selection } from "./warehousePresentation";
import "./styles.css";
import "./directory.css";

const pages = ["Warehouse", "Fleet", "Jobs", "Operations AI"] as const;
const pageIcons = { Warehouse: "building-warehouse", Fleet: "robot", Jobs: "clipboard-list", "Operations AI": "activity" } as const;
type Page = typeof pages[number];

type Workspace = {
  page: Page;
  navigate: (page: Page) => void;
  selection: Selection | null;
  selectEntity: (selection: Selection | null) => void;
  feed: WarehouseFeed;
};

type SessionActions = {
  returning: boolean; returnError: string | null;
  onReturn: () => void; onControl: (action: WarehouseAction) => void;
};
const WorkspaceContext = createContext<Workspace | null>(null);

export function useV2Workspace(): Workspace {
  const workspace = useContext(WorkspaceContext);
  if (!workspace) throw new Error("Warehouse V2 workspace is unavailable.");
  return workspace;
}

const initialFeed: WarehouseFeed = { phase: "connecting", status: null, frame: null, pending: null, controlWarning: null };
const connectionLabel = {
  connecting: "Connecting to Warehouse V2…",
  reconnecting: "Reconnecting. Showing only the last confirmed data.",
  live: "Warehouse V2 connected.",
  unavailable: "Warehouse V2 is unavailable. Retrying session status…",
  expired: "Visitor session ended. Clear this site's cookies to begin a new visit.",
};
const modeLabel = {
  deterministic: "Deterministic dispatch",
  checking_assistance: "Checking AI assistance",
  ai_assisted: "AI-assisted dispatch",
} as const;

const pendingLabel: Record<WarehouseAction, string> = {
  play: "Starting…", pause: "Pausing…", stop: "Stopping…", restart: "Restarting…",
};

function Toolbar({ page, feed, returning, onControl }: {
  page: Page; feed: WarehouseFeed; returning: boolean; onControl: (action: WarehouseAction) => void;
}) {
  const live = feed.phase === "live";
  const state = feed.frame?.state;
  const running = state === "running";
  const primary = running ? "pause" : "play";
  const blocked = (action: WarehouseAction) =>
    !live || returning || !!feed.pending || state === undefined || !allowsWarehouseAction(state, action);
  const warning = feed.controlWarning;
  const acknowledgment = warning && warning !== "run_changed"
    ? warning[0].toUpperCase() + warning.slice(1) + " acknowledgment was not received."
    : null;
  const feedback = acknowledgment ? (live && state ? "Go reports " + state + ". " : "") + acknowledgment
    : warning === "run_changed" ? "Run changed during confirmation."
    : feed.pending ? pendingLabel[feed.pending]
    : state === "stopped" && live ? "Restart to play" : !live ? "Waiting for Go" : "";

  function trigger(action: WarehouseAction): void {
    if (!blocked(action)) onControl(action);
  }

  return <header className="v2-toolbar">
    <div className="v2-page-title"><span>FleetOps</span><span aria-hidden="true">/</span><h1>{page}</h1></div>
    <div className="v2-toolbar-state">
      {live && feed.frame && <span className="v2-mode" data-tone={feed.frame.mode === "ai_assisted" ? "assisted" : undefined}><Icon name="activity" size={16} />{modeLabel[feed.frame.mode]}</span>}
      <p className={'v2-connection v2-connection-' + feed.phase + (live && running ? ' is-running' : '')} role="status">
        {live && state ? "Simulation " + state
          : feed.pending && feed.phase === "reconnecting" && !feed.controlWarning ? "Confirming with Go…" : connectionLabel[feed.phase]}
      </p>
      {!live && state && <span>Last confirmed: {state}</span>}
      <span id="v2-control-note" className="v2-control-note" role="status"
        data-feedback={warning ? "unconfirmed" : feed.pending ? "pending" : undefined}>{feedback}</span>
    </div>
    <div className="v2-sim-controls" role="group" aria-label="Simulation controls" aria-describedby="v2-control-note">
      <button type="button" aria-disabled={blocked(primary)} onClick={() => trigger(primary)}>
        <Icon name={running ? "pause" : "player-play"} size={16} />{running ? "Pause" : "Play"}
      </button>
      <button type="button" aria-disabled={blocked("stop")} onClick={() => trigger("stop")}>
        <Icon name="player-stop" size={16} />Stop
      </button>
      <button type="button" aria-disabled={blocked("restart")} onClick={() => trigger("restart")}>Restart</button>
    </div>
  </header>;
}

function SessionView({ returning, returnError, onReturn, onControl }: SessionActions): JSX.Element {
  const { page, navigate, feed, selection, selectEntity } = useV2Workspace();
  const main = useRef<HTMLElement>(null);
  const previousPage = useRef(page);

  useLayoutEffect(() => {
    if (previousPage.current === page) return;
    previousPage.current = page;
    if (document.activeElement === document.body) main.current?.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  }, [page]);

  function inspectRelated(entity: Selection): void {
    if (!feed.frame || !findEntity(feed.frame.snapshot, entity)) return;
    selectEntity(entity);
    navigate(entity.kind === "job" ? "Jobs" : "Fleet");
  }

  return <div className="v2-shell">
    <a className="skip-link" href="#main-content">Skip to main content</a>
    <aside className="v2-rail">
      <div className="v2-brand-mark" aria-label="FleetOps V2"><Icon name="layout-grid" size={23} /></div>
      <nav aria-label="Warehouse V2 pages">
        {pages.map(item => <button type="button" key={item} aria-current={item === page ? "page" : undefined}
          onClick={() => navigate(item)}><Icon name={pageIcons[item]} size={19} /><span>{item}</span></button>)}
      </nav>
      <button className="v2-legacy-link" type="button" disabled={returning || !!feed.pending || feed.phase !== "live"}
        aria-label="Return to V1 demo" onClick={onReturn}>
        <Icon name="arrow-right" size={15} />{returning ? "Returning…" : "V1 demo"}
      </button>
    </aside>
    <Toolbar page={page} feed={feed} returning={returning} onControl={onControl} />
    <main ref={main} id="main-content" className="v2-workspace" tabIndex={-1}>
      {returnError && <p className="v2-return-error" role="alert">{returnError}</p>}
      <Warehouse frame={feed.frame} selection={selection} selectEntity={selectEntity} viewFleet={() => navigate("Fleet")} hidden={page !== "Warehouse"} />
      {(page === "Fleet" || page === "Jobs") && <EntityDirectory key={page} page={page} feed={feed}
        selection={selection} selectEntity={selectEntity} viewWarehouse={() => navigate("Warehouse")} inspectRelated={inspectRelated} />}
      {page === "Operations AI" && <section className="v2-panel v2-empty" aria-label={page}>
        <h2>{page}</h2><p>This view is planned for a later phase of the frontend rebuild.</p>
        <button type="button" className="v2-inspector-link" onClick={() => navigate("Warehouse")}>Return to Warehouse</button>
      </section>}
    </main>
  </div>;
}

function WorkspaceView({ feed, ...actions }: { feed: WarehouseFeed } & SessionActions): JSX.Element {
  const [page, navigate] = useState<Page>("Warehouse");
  const generation = feed.status?.generation ?? feed.frame?.generation;
  const [selected, setSelected] = useState<{ generation: string | undefined; entity: Selection } | null>(null);
  const selection = selected && selected.generation === generation && feed.frame && findEntity(feed.frame.snapshot, selected.entity)
    ? selected.entity : null;
  if (selected && !selection) setSelected(null);
  const selectEntity = useCallback((entity: Selection | null) => {
    setSelected(entity ? { generation, entity } : null);
  }, [generation]);
  return <WorkspaceContext.Provider value={{ page, navigate, selection, selectEntity, feed }}>
    <SessionView {...actions} />
  </WorkspaceContext.Provider>;
}

export default function V2Shell({ previous = "blocked-reassignment", onLegacy }: {
  previous?: ScenarioID; onLegacy: () => void;
}): JSX.Element {
  const [feed, setFeed] = useState(initialFeed);
  const [returning, setReturning] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);
  const returnInFlight = useRef(false);

  const session = useRef<ReturnType<typeof subscribeWarehouse> | null>(null);
  useEffect(() => {
    const current = subscribeWarehouse(setFeed, onLegacy);
    session.current = current;
    return () => {
      session.current = null;
      current.close();
    };
  }, [onLegacy]);

  function runControl(action: WarehouseAction): void {
    if (!returnInFlight.current && feed.frame) void session.current?.control(action, feed.frame.generation);
  }

  async function returnToLegacy(): Promise<void> {
    if (returnInFlight.current || !feed.status || !session.current?.canControl(feed.status.generation)) return;
    returnInFlight.current = true;
    setReturning(true);
    setReturnError(null);
    try {
      await selectScenario(previous, feed.status.generation);
      onLegacy();
    } catch (error) {
      setReturnError(error instanceof DemoError && error.authentication ? error.message
        : "V1 switch was not confirmed. Check the session status before trying again.");
    } finally {
      returnInFlight.current = false;
      setReturning(false);
    }
  }

  return <WorkspaceView feed={feed} returning={returning} returnError={returnError} onControl={runControl}
    onReturn={() => { void returnToLegacy(); }} />;
}
