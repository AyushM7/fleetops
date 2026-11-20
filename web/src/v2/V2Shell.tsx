import { createContext, useContext, useEffect, useRef, useState, type JSX } from "react";
import { DemoError, selectScenario } from "../restart";
import type { ScenarioID } from "../scenarios";
import { subscribeWarehouse, type WarehouseFeed } from "./session";

const pages = ["Warehouse", "Fleet", "Jobs", "Operations AI"] as const;
type Page = typeof pages[number];
type Selection = { kind: "robot" | "job" | "station"; id: string };

type Workspace = {
  page: Page;
  navigate: (page: Page) => void;
  selection: Selection | null;
  selectEntity: (selection: Selection | null) => void;
  feed: WarehouseFeed;
};

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

function SessionView(): JSX.Element {
  const { page, navigate, feed } = useV2Workspace();
  return <>
    <nav aria-label="Warehouse V2 pages">
      {pages.map(item => <button type="button" key={item} aria-current={item === page ? "page" : undefined}
        onClick={() => { navigate(item); }}>{item}</button>)}
    </nav>
    <main id="main-content" tabIndex={-1}>
      <h1>{page === "Warehouse" ? "Warehouse V2" : page}</h1>
      <p role="status">{connectionLabel[feed.phase]}</p>
      {feed.frame && <p>{feed.phase === "live" ? "Simulation" : "Last confirmed"} tick {feed.frame.snapshot.tick}</p>}
      {feed.phase === "live" && feed.frame?.mode === "deterministic" && <p>Deterministic dispatch</p>}
    </main>
  </>;
}

function WorkspaceView({ feed }: { feed: WarehouseFeed }): JSX.Element {
  const [page, navigate] = useState<Page>("Warehouse");
  const [selection, selectEntity] = useState<Selection | null>(null);
  return <WorkspaceContext.Provider value={{ page, navigate, selection, selectEntity, feed }}>
    <SessionView />
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

  return <div className="app-shell">
    <a className="skip-link" href="#main-content">Skip to warehouse</a>
    <header className="topbar">
      <span className="brand">FleetOps</span>
      <button type="button" className="secondary-button" disabled={returning || feed.phase !== "live"}
        onClick={() => { void returnToLegacy(); }}>{returning ? "Returning to V1…" : "Return to V1 demo"}</button>
      {returnError && <p role="alert">{returnError}</p>}
    </header>
    <WorkspaceView key={feed.status?.generation ?? feed.frame?.generation} feed={feed} />
  </div>;
}
