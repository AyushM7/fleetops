import { useState } from "react";
import { Icon } from "./WarehouseIcon";
import { cellLabel, isPriorityOverdue, jobLabels, jobOutcomeLabels, type PickEntity, type Selection } from "./warehousePresentation";
import type { Job, Snapshot } from "./telemetry";

const filters = {
  all: "All jobs", pending: "Pending", assigned: "Assigned", in_progress: "In progress",
  overdue: "Overdue priority", done: "Retained completions",
} as const;
type Filter = keyof typeof filters;

function JobRow({ job, selected, pick }: { job: Job; selected: boolean; pick: PickEntity }) {
  const overdue = isPriorityOverdue(job);
  const outcome = job.status === "done" && job.outcome ? jobOutcomeLabels[job.outcome] : null;
  const tone = overdue ? "fault" : job.status === "pending" ? "waiting" : undefined;
  return <li>
    <button type="button" className="v2-job-row" aria-pressed={selected} data-tone={tone}
      onClick={event => pick({ kind: "job", id: job.id }, event.currentTarget)}>
      <span className="v2-job-row-id"><Icon name="package" size={17} /><strong>{job.id}</strong>
        <span className="v2-directory-muted">{job.type === "PRIORITY" ? "Priority" : "Normal"}</span>
      </span>
      <span className="v2-job-row-state">
        <span className="v2-status-chip">{jobLabels[job.status]}</span>
        {overdue && <strong className="v2-overdue">Overdue</strong>}
        {outcome && <span className="v2-directory-muted">{outcome}</span>}
      </span>
      <span className="v2-job-row-endpoints">
        <span>Pickup <strong>{cellLabel(job.from)}</strong></span>
        <span>Drop-off <strong>{cellLabel(job.to)}</strong></span>
      </span>
      <span className="v2-job-row-assignment">
        <span>{job.assignedRobot ? "Robot " + job.assignedRobot : "Unassigned"}</span>
        {job.deadlineTick !== undefined && <span className="v2-directory-muted">Deadline tick {job.deadlineTick}</span>}
      </span>
    </button>
  </li>;
}

export function Jobs({ snapshot, selection, pick }: {
  snapshot: Snapshot; selection: Selection | null; pick: PickEntity;
}) {
  const [filter, setFilter] = useState<Filter>("all");
  const completed = snapshot.completedJobs ?? 0;
  const open = snapshot.jobs.filter(job => job.status !== "done");
  const retained = snapshot.jobs.filter(job => job.status === "done")
    .sort((a, b) => (b.completedTick ?? -1) - (a.completedTick ?? -1));
  const visible = filter === "all" ? open
    : open.filter(job => filter === "overdue" ? isPriorityOverdue(job) : job.status === filter);
  const showOpen = filter !== "done";
  const showRetained = filter === "all" || filter === "done";

  function rows(jobs: Job[], label: string) {
    return <ul className="v2-job-list" aria-label={label}>
      {jobs.map(job => <JobRow key={job.id} job={job} pick={pick} selected={selection?.kind === "job" && selection.id === job.id} />)}
    </ul>;
  }

  return <section className="v2-panel v2-directory-panel" aria-labelledby="v2-jobs-title">
    <header className="v2-directory-header">
      <h2 id="v2-jobs-title" tabIndex={-1}>Job queue</h2>
      <p>{completed} completed {completed === 1 ? "delivery" : "deliveries"}</p>
      <label className="v2-job-filter">Job status
        <select value={filter} onChange={event => setFilter(event.currentTarget.value as Filter)}>
          {Object.entries(filters).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select>
      </label>
    </header>
    {showOpen && <section className="v2-jobs-group" aria-labelledby="v2-open-jobs">
      <h3 id="v2-open-jobs">Open jobs <span>{visible.length}</span></h3>
      {visible.length ? rows(visible, "Open jobs") : <p className="v2-directory-empty">
        {filter === "all" ? "No open jobs in this snapshot." : "No open jobs match this filter."}
      </p>}
    </section>}
    {showRetained && <section className="v2-jobs-group" aria-labelledby="v2-retained-jobs">
      <h3 id="v2-retained-jobs">Retained completions <span>{retained.length}</span></h3>
      {retained.length ? rows(retained, "Retained completions")
        : <p className="v2-directory-empty">No completed jobs are retained in this snapshot.</p>}
    </section>}
  </section>;
}
