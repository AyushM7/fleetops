import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import { parseRecoveryItems, parseRecoveryReport, RecoveryInvestigations, RecoveryProposalList } from "./RecoveryApprovals";

const row = {
  runId: "A" + "1".repeat(32), state: "pending", execution: "none",
  selection: { jobId: "J2", fromRobotId: "R1", toRobotId: "R3" },
  basis: "pickup_cost", metrics: { pickupSteps: 4, requiredBattery: 32 },
  incidents: [{ robotId:"R1",type:"traffic_blocked",severity:"high" }],
};
const render = (value: unknown, blocked = false, pending: string | null = null) =>
  renderToStaticMarkup(<RecoveryProposalList items={parseRecoveryItems(value)} blocked={blocked} pending={pending} decide={() => {}} />);

it("describes the empty recovery list without a version label", () => {
  expect(render([])).toContain("No verified recovery proposals for this run.");
});

it("renders authoritative evidence and explicit actions for pending V3 decisions", () => {
  const html = render([row]);
  for (const label of ["Reassign J2 from R1 to R3", "traffic blocked", "Shortest eligible pickup", "Required battery 32%", "Approve J2", "Reject J2"]) expect(html).toContain(label);
});

it("disables writes while a decision is pending or the feed is stale", () => {
  expect(render([row], false, row.runId)).toContain('disabled=""');
  expect(render([row], true)).toContain('disabled=""');
});

it("shows approved execution, expiry and rejection without offering another decision", () => {
  for (const [state,execution,label] of [["approved","succeeded","Approved and executed"],["expired","none","Expired"],["rejected","none","Rejected"]]) {
    const html = render([{ ...row, state, execution }]);
    expect(html).toContain(label);
    expect(html).not.toContain("Approve J2");
  }
});

it("rejects path injection and unsafe evidence", () => {
  expect(() => parseRecoveryItems([{ ...row, runId: "../approve" }])).toThrow();
  expect(() => parseRecoveryItems([{ ...row, metrics: { requiredBattery: "32" } }])).toThrow();
});

it("locks an attempted decision while Go still reports it pending", () => {
  const html = renderToStaticMarkup(<RecoveryProposalList items={parseRecoveryItems([row])} blocked={false}
    pending={null} submitted={new Set([row.runId])} decide={() => {}} />);
  expect(html.match(/disabled=""/g)).toHaveLength(2);
});

it("shows the current job owner and robot after successful execution", () => {
  const items = parseRecoveryItems([{ ...row, state: "approved", execution: "succeeded" }]);
  const snapshot = {
    tick: 10,
    jobs: [{ id: "J2", status: "assigned" as const, type: "NORMAL" as const, assignedRobot: "R3",
      from: { x: 1, y: 1 }, to: { x: 2, y: 1 } }],
    robots: [{ id: "R3", x: 2, y: 2, status: "idle" as const, activity: "AVAILABLE" as const, role: "transport" as const,
      battery: 71, jobId: "J2", blocked: false, waitTicks: 0 }],
  };
  const html = renderToStaticMarkup(<RecoveryProposalList items={items} blocked={false} pending={null} snapshot={snapshot} decide={() => {}} />);
  expect(html).toContain("Owner R3");
  expect(html).toContain("71% battery");
});

it("distinguishes confirmed live abstention, failed live runs, and offline outcomes", () => {
  const investigations = [
    { runId: row.runId, source: "live-model", status: "abstained", providerConfirmed: true },
    { runId: "A" + "2".repeat(32), source: "live-model", status: "failed", providerConfirmed: false },
    { runId: "A" + "3".repeat(32), source: "deterministic", status: "proposal_not_admitted", providerConfirmed: false },
  ];
  const report = parseRecoveryReport({ live: true, proposals: [], investigations });
  const html = renderToStaticMarkup(<RecoveryInvestigations report={report} />);
  for (const label of ["Abstained", "OpenAI response confirmed", "Investigation failed",
    "response unconfirmed", "Deterministic result", "Go did not admit the proposal"]) expect(html).toContain(label);
  expect(html.match(/OpenAI response confirmed/g)).toHaveLength(1);
  expect(renderToStaticMarkup(<RecoveryInvestigations report={{ ...report, live: false, investigations: [] }} />))
    .toContain("Live OpenAI recovery is off.");
  expect(() => parseRecoveryReport({
    live: true, proposals: [], investigations: [{ ...investigations[2], providerConfirmed: true }],
  })).toThrow();
});
