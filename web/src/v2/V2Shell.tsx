import { createContext, useContext, useEffect, useRef, useState, type JSX } from "react";
import { DemoError, selectScenario } from "../restart";
import type { ScenarioID } from "../scenarios";
import { subscribeWarehouse, type WarehouseFeed } from "./session";
import { Warehouse } from "./Warehouse";
import { Icon } from "./WarehouseIcon";
import type { Selection } from "./warehousePresentation";
import "./styles.css";

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

type LegacyExit = { returning: boolean; returnError: string | null; onReturn: () => void };
const WorkspaceContext = createContext<Workspace | null>(null);

export function useV2Workspace(): Workspace {
  const workspace = useContext(WorkspaceContext);
  if (!workspace) throw new Error("Warehouse V2 workspace is unavailable.");
  return workspace;
}

const initialFeed: WarehouseFeed = { phase: "connecting", status: null, frame: null };
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

function Toolbar({ page, feed }: { page: Page; feed: WarehouseFeed }) {
  const live = feed.phase === "live" ? feed.frame : null;
  const running = live?.state === "running";
  return <header className="v2-toolbar">
    <div className="v2-page-title"><span>FleetOps</span><span aria-hidden="true">/</span><h1>{page}</h1></div>
    <div className="v2-toolbar-state">
      {live && <span className="v2-mode"><Icon name="activity" size={16} />{modeLabel[live.mode]}</span>}
      <p className={'v2-connection v2-connection-' + feed.phase + (running ? ' is-running' : '')} role="status">
        {live ? "Simulation " + live.state : connectionLabel[feed.phase]}
      </p>
    </div>
    <div className="v2-sim-controls" role="group" aria-label="Simulation controls" aria-describedby="v2-control-note">
      <span id="v2-control-note">Controls pending</span>
      <button type="button" disabled title="Simulation controls are not connected">
        <Icon name={running ? "pause" : "player-play"} size={16} />{running ? "Pause" : "Play"}
      </button>
      <button type="button" disabled title="Simulation controls are not connected">
        <Icon name="player-stop" size={16} />Stop
      </button>
    </div>
  </header>;
}

function SessionView({ returning, returnError, onReturn }: LegacyExit): JSX.Element {
  const { page, navigate, feed, selection, selectEntity } = useV2Workspace();
  return <div className="v2-shell">
    <a className="skip-link" href="#main-content">Skip to warehouse</a>
    <aside className="v2-rail">
      <div className="v2-brand-mark" aria-label="FleetOps V2"><Icon name="layout-grid" size={23} /></div>
      <nav aria-label="Warehouse V2 pages">
        {pages.map(item => <button type="button" key={item} aria-current={item === page ? "page" : undefined}
          onClick={() => navigate(item)}><Icon name={pageIcons[item]} size={19} /><span>{item}</span></button>)}
      </nav>
      <button className="v2-legacy-link" type="button" disabled={returning || feed.phase !== "live"}
        aria-label="Return to V1 demo" onClick={onReturn}>
        <Icon name="arrow-right" size={15} />{returning ? "Returning…" : "V1 demo"}
      </button>
    </aside>
    <Toolbar page={page} feed={feed} />
    <main id="main-content" className="v2-workspace" tabIndex={-1}>
      {returnError && <p className="v2-return-error" role="alert">{returnError}</p>}
      <Warehouse frame={feed.frame} selection={selection} selectEntity={selectEntity} viewFleet={() => navigate("Fleet")} hidden={page !== "Warehouse"} />
      {page !== "Warehouse" && <section className="v2-panel v2-empty" aria-label={page}>
        <h2>{page}</h2><p>This view is planned for a later phase of the frontend rebuild.</p>
        <button type="button" className="v2-inspector-link" onClick={() => navigate("Warehouse")}>Return to Warehouse</button>
      </section>}
    </main>
  </div>;
}

function WorkspaceView({ feed, ...exit }: { feed: WarehouseFeed } & LegacyExit): JSX.Element {
  const [page, navigate] = useState<Page>("Warehouse");
  const [selection, selectEntity] = useState<Selection | null>(null);
  return <WorkspaceContext.Provider value={{ page, navigate, selection, selectEntity, feed }}>
    <SessionView {...exit} />
  </WorkspaceContext.Provider>;
}

export default function V2Shell({ previous = "blocked-reassignment", onLegacy }: {
  previous?: ScenarioID; onLegacy: () => void;
}): JSX.Element {
  const [feed, setFeed] = useState(initialFeed);
  const [returning, setReturning] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);
  const returnInFlight = useRef(false);

  useEffect(() => subscribeWarehouse(setFeed, onLegacy), [onLegacy]);

  async function returnToLegacy(): Promise<void> {
    if (returnInFlight.current || feed.phase !== "live" || !feed.status) return;
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

  return <WorkspaceView key={feed.status?.generation ?? feed.frame?.generation} feed={feed} returning={returning}
    returnError={returnError} onReturn={() => { void returnToLegacy(); }} />;
}
