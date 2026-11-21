import { Fleet } from "./Fleet";
import { Jobs } from "./Jobs";
import { WarehouseInspector, useInspectorFocus } from "./WarehouseInspector";
import type { WarehouseFeed } from "./session";
import { findEntity, type Selection } from "./warehousePresentation";

export function EntityDirectory({ page, feed, selection, selectEntity, viewWarehouse, inspectRelated }: {
  page: "Fleet" | "Jobs"; feed: WarehouseFeed; selection: Selection | null;
  selectEntity: (selection: Selection | null) => void; viewWarehouse: () => void;
  inspectRelated: (selection: Selection) => void;
}) {
  const { surface, pick, close, restoreFocus } = useInspectorFocus(selectEntity, "h2", true);
  const snapshot = feed.frame?.snapshot;
  const entity = snapshot && selection?.kind === (page === "Fleet" ? "robot" : "job")
    ? findEntity(snapshot, selection) : null;
  const job = entity?.kind === "robot" ? snapshot?.jobs.find(item => item.id === entity.robot.jobId) : undefined;
  const canInspectAssigned = entity?.kind === "job" && snapshot?.robots.some(robot => robot.id === entity.job.assignedRobot);
  const lastConfirmedTick = feed.phase === "live" ? undefined : snapshot?.tick;

  return <div ref={surface} className={"v2-directory" + (entity ? " has-inspector" : "")}>
    {lastConfirmedTick !== undefined && <p className="v2-directory-notice" role="status">
      Showing last confirmed snapshot at tick {lastConfirmedTick}.
    </p>}
    {snapshot ? page === "Fleet" ? <Fleet snapshot={snapshot} selection={selection} pick={pick} />
      : <Jobs snapshot={snapshot} selection={selection} pick={pick} />
      : <section className="v2-panel v2-empty">
        <h2 tabIndex={-1}>{page === "Fleet" ? "Robots" : "Job queue"}</h2>
        <p>Waiting for confirmed {page === "Fleet" ? "fleet" : "job"} telemetry.</p>
      </section>}
    <WarehouseInspector entity={entity} snapshot={snapshot} job={job} onWarehouse={viewWarehouse} lastConfirmedTick={lastConfirmedTick}
      canInspectAssigned={canInspectAssigned} inspect={inspectRelated} close={close} restoreFocus={restoreFocus} />
  </div>;
}
