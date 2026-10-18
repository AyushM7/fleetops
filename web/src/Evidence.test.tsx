/// <reference types="vite/client" />
import { renderToString } from "react-dom/server";
import { afterEach, expect, it, vi } from "vitest";
import observations from "../../docs/evidence/demo-08/observations.json?raw";
import { Evidence, parseEvidence, type EvidenceContext } from "./Evidence";
import { InboxView } from "./ApprovalInbox";
import { pollDemoStatus } from "./restart";
import type { Proposal } from "./proposals";

const rows = JSON.parse(observations);
const generation = "a".repeat(64);
const p: Proposal = { id: "P-example", action: "reassign_unstarted_job", robotId: "", stationId: "", jobId: "J1", fromRobotId: "R1", toRobotId: "R3", state: "pending", execution: "none", submittedBy: "proposer", currentRuntime: true, createdAt: "2026-10-17T12:00:00Z", expiresAt: "2030-10-17T12:00:00Z" };
function context(index = 0): EvidenceContext {
  const row = rows[index];
  const evidence = parseEvidence({ generation, runtimeId: row.runtimeId, scenarioId: row.id, proposalId: index === 0 || index === 2 ? p.id : "", observation: row.snapshot },generation,row.id);
  return { generation, scenarioId: row.id, evidence, current: row.snapshot, available: true };
}
const html = (c: EvidenceContext, proposal?: Proposal) => renderToString(<Evidence context={c} proposal={proposal} />).replace(/<!--.*?-->/g,"");
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

it("grounds evidence in actual Go values and separates all seven stages", () => {
  const output = html(context(),p);
  for (const text of ["traffic_blocked (high) at tick 42", "battery 100%", "waiting 42 ticks", "battery 98%", "position (4, 3)", "owner R1", "ReassignUnstartedJob", "J1", "R1 → R3", "The obstruction remains", "strict reasoning schema", "revalidated", "Independent operator approval", "No execution recorded", "No live LLM"]) expect(output).toContain(text);
  for (const stage of ["Go observation / classification", "Recommendation source", "Host grounding / policy", "Proposal / Go validation", "Human decision", "Recorded Go execution", "Observed simulator outcome"]) expect(output).toContain(stage);
  expect(output).not.toContain("GPT"); expect(output).not.toContain("confidence");
});
it("changes grounded explanation for charging and no-action presets without fake approvals", () => {
  const charging = { ...p, action: "set_charging" as const, jobId: "", fromRobotId: "", toRobotId: "", robotId: "R1", stationId: "S1" };
  const output = html(context(2),charging);
  for (const text of ["tick 1000", "SetCharging", "battery 50%", "position (0, 0)", "occupied by none", "Battery increases require later simulation ticks"]) expect(output).toContain(text);
  expect(output).not.toContain("Go admitted R3");
  const none = html(context(1));
  expect(none).toContain("Go admitted 0 transfer candidates"); expect(none).toContain("battery 15%");
  const healthy = context(3); healthy.current = { ...healthy.current!, tick: 1, robots: healthy.current!.robots.map(r => ({ ...r, status: "moving" })) };
  expect(html(healthy)).toContain("6 moving robots; 8 open jobs at tick 1");
  for (const c of [context(1),healthy]) {
    const inbox = renderToString(<InboxView feed={{phase:"ready",proposals:[],updatedAt:new Date()}} busy={null} receipt={null} runtimeAvailable evidenceContext={c} decide={vi.fn()} refresh={vi.fn()} refreshStatus={vi.fn()} />);
    expect(inbox).toContain("No proposal; no approval requested");
    expect(inbox).not.toContain("approve-button");
    expect(inbox).not.toContain("No execution recorded");
  }
});
it("retires previous-generation, mismatched, missing, stale and poisoned evidence", () => {
  const c = context();
  for (const changed of [ {generation:"b".repeat(64)}, {scenarioId: "routine-charging" as const}, {evidence:undefined}, {current:null}, {available:false}, {current:{...c.current!,poisoned:true}}, {current:{...c.current!,tick:41}} ]) {
    const output = html({...c,...changed},p);
    expect(output).toContain("Evidence unavailable or stale");
    expect(output).not.toContain("Host grounding / policy");
    expect(output).not.toContain("Go admitted R3");
  }
  expect(html(c,{...p,id:"other"})).toContain("unavailable or stale");
  expect(html(c,{...p,currentRuntime:false})).toContain("unavailable or stale");
  expect(html(c,{...p,toRobotId:"invented"})).toContain("unavailable or stale");
});
it("keeps original evidence historical after real later observations and separates execution from delivery", () => {
  const c = context(); c.current = {...c.current!,tick:45,jobs:c.current!.jobs.map(j=>({...j,status:"done",assignedRobot:"R3"}))};
  const output = html(c,{...p,state:"approved",execution:"succeeded"});
  expect(output).toContain("Original Go observation · tick 42");
  expect(output).toContain("Current Go telemetry · tick 45");
  expect(output).toContain("Original evidence is historical");
  expect(output).toContain("Stored Go execution succeeded");
  expect(output).toContain("Job J1: done at tick 45");
  expect(output).toContain("No causal benefit");
  expect(html(c,{...p,state:"approved",execution:"unknown"})).toContain("Execution failed or remains uncertain");
});
it("rejects stale envelopes and fabricated candidate evidence without provider or transport leakage", async () => {
  const row = rows[0];
  const raw = { generation, runtimeId:row.runtimeId, scenarioId:row.id, proposalId:p.id, observation:row.snapshot };
  expect(() => parseEvidence({...raw,generation:"b".repeat(64)},generation,row.id)).toThrow();
  const bad = structuredClone(raw); bad.observation.reassignmentCandidates[0].toRobotId = "invented";
  expect(() => parseEvidence(bad,generation,row.id)).toThrow("invalid");
  vi.useFakeTimers();
  vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({generation,ready:true,scenarioId:row.id,evidence:{...raw,generation:"b".repeat(64)}}))));
  const emit = vi.fn(); const stop = pollDemoStatus(emit,vi.fn());
  await vi.advanceTimersByTimeAsync(0); stop();
  expect(emit.mock.calls[0][0]).toBeNull();
  expect(JSON.stringify(emit.mock.calls)).not.toContain("provider");
});
it("evidence rendering cannot call network, approve, tick or execute", () => {
  const network = vi.fn(() => {throw new Error("No network");}); vi.stubGlobal("fetch",network);
  const output = html(context(),p);
  expect(output).not.toContain("<button");
  expect(output).not.toContain("<script");
  expect(network).not.toHaveBeenCalled();
});

it.each([0, 1, 2, 3])("keeps preset %i deterministic and rejects previous-generation evidence even when IDs are reused", index => {
  const c = context(index);
  const proposal = index === 0 ? p : index === 2 ? { ...p, action: "set_charging" as const,
    jobId: "", fromRobotId: "", toRobotId: "", robotId: "R1", stationId: "S1" } : undefined;
  const current = html(c, proposal);
  expect(current).toContain("Deterministic presentation · No live LLM");
  expect(current).not.toMatch(/live OpenAI inference|GPT-powered|GPT-6|completed model analysis/i);
  expect(current).toContain(index === 0 || index === 2 ? "Deterministic local Mastra adapter; no paid model inference" : "No adapter or model invoked");
  const nextGeneration = "b".repeat(64);
  const retired = html({ ...c, generation: nextGeneration }, proposal);
  expect(retired).toContain("Evidence unavailable or stale");
  expect(retired).not.toContain("Original Go observation");
  expect(() => parseEvidence(c.evidence, nextGeneration, c.scenarioId)).toThrow("identity");
  const fresh = parseEvidence({ ...c.evidence, generation: nextGeneration, runtimeId: `P${"b".repeat(32)}` }, nextGeneration, c.scenarioId);
  const replacement = html({ ...c, generation: nextGeneration, evidence: fresh }, proposal);
  expect(replacement).toContain("Original Go observation");
  expect(replacement).toContain("No live LLM");
});
