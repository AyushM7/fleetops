import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToString } from "react-dom/server";
import { createInterface } from "node:readline";
import { fetchFleet, fetchPending, fetchProposal, decideProposal, Dashboard, InboxView } from "../dist/contract/contract-entry.js";

assert.equal(process.version, "v24.21.0");
const tokens = JSON.parse(process.env.FLEETOPS_P404_TOKENS);
const endpoint = new URL(process.env.FLEETOPS_P404_URL);
assert.equal(endpoint.hostname, "127.0.0.1");
const reassign = process.env.FLEETOPS_P404_PROFILE === "reassignment";
const network = globalThis.fetch;
globalThis.fetch = (path, options) => {
  assert.equal(path, "/graphql");
  return network(new URL(path, endpoint), options);
};
const lines = createInterface({ input: process.stdin });
const iterator = lines[Symbol.asyncIterator]();
const control = async command => { console.log(command); assert.equal((await iterator.next()).value, "OK"); };
const signal = () => new AbortController().signal;
const read = () => fetchFleet(tokens.viewer, signal());
const render = snapshot => renderToString(createElement(Dashboard, { feed: { phase: "live", snapshot, updatedAt: new Date() } })).replace(/<!--.*?-->/g, "");
async function submit(expiresAt = new Date(Date.now() + 60_000).toISOString()) {
  const observed = await fetch("/graphql", { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${tokens.proposer}` }, body: JSON.stringify({ query: "{fleetState{observationId}}" }) });
  const current = await observed.json(); assert.equal(current.errors, undefined);
  const query = reassign ? "mutation($input:ReassignUnstartedJobInput!){submitReassignment(input:$input){proposalId error auditCommitted}}"
    : "mutation($input:SetChargingInput!){submitProposal(input:$input){proposalId error auditCommitted}}";
  const input = reassign ? { jobId: "J1", fromRobotId: "R1", toRobotId: "R3", expiresAt } : { robotId: "R1", stationId: "S1", expiresAt };
  input.expectedObservationId = current.data.fleetState.observationId;
  const response = await fetch("/graphql", { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${tokens.proposer}` }, body: JSON.stringify({ query, variables: { input } }) });
  const body = await response.json(); assert.equal(body.errors, undefined);
  const result = body.data[reassign ? "submitReassignment" : "submitProposal"];
  assert.equal(result.error, null); assert.equal(result.auditCommitted, true); return result.proposalId;
}
try {
  const before = await read();
  if (reassign) { assert.equal(before.tick, 42); assert.match(render(before), /High severity/); assert.match(render(before), /R1 · Traffic blocked/); }
  for (const role of ["viewer", "proposer"]) await assert.rejects(fetchPending(tokens[role], signal()), { kind: "forbidden" });
  await assert.rejects(fetchFleet("invalid-credential", signal()), { kind: "authentication" });
  const rejectedID = await submit(); const id = await submit(); const staleID = await submit(); const expiredID = await submit();
  const pending = await fetchPending(tokens.operator, signal()); assert.equal(pending.length, 4);
  const proposal = pending.find(p => p.id === id); assert.ok(proposal);
  assert.equal(proposal.action, reassign ? "reassign_unstarted_job" : "set_charging");
  const inbox = renderToString(createElement(InboxView, { feed: { phase: "ready", proposals: pending, updatedAt: new Date() }, busy: null, receipt: null, runtimeAvailable: true, decide() {}, refresh() {}, refreshStatus() {} }));
  assert.match(inbox, reassign ? /ReassignUnstartedJob/ : /SetCharging/);
  for (const role of ["viewer", "proposer", "self"]) {
    const denied = await decideProposal(tokens[role], id, "approve", signal()); assert.ok(denied.error); assert.equal(denied.executionAttempted, false);
  }
  const rejected = await decideProposal(tokens.operator, rejectedID, "reject", signal());
  assert.equal(rejected.state, "rejected"); assert.equal(rejected.executionAttempted, false); assert.deepEqual(await read(), before);
  const result = await decideProposal(tokens.operator, id, "approve", signal());
  assert.equal(result.error, null); assert.equal(result.execution, "succeeded"); assert.equal(result.auditCommitted, true);
  const latest = await fetchProposal(tokens.operator, id, signal()); assert.equal(latest.state, "approved");
  const receipt = { id, proposal, decision: "approve", result, latest, refreshing: false, refreshFailed: false };
  const receiptHTML = renderToString(createElement(InboxView, { feed: { phase: "ready", proposals: [], updatedAt: new Date() }, busy: null, receipt, runtimeAvailable: true, decide() {}, refresh() {}, refreshStatus() {} }));
  assert.match(receiptHTML, reassign ? /not delivery completion/ : /Charging started/);
  const transferred = await read();
  if (reassign) {
    assert.equal(transferred.robots.find(r => r.id === "R1").jobId, "");
    assert.equal(transferred.robots.find(r => r.id === "R3").jobId, "J1");
    assert.equal(transferred.jobs[0].assignedRobot, "R3"); assert.equal(transferred.jobs[0].status, "assigned");
    assert.match(render(transferred), /awaiting pickup/); assert.doesNotMatch(render(transferred), /Delivered/);
    await control("ADVANCE"); const pickup = await read(); assert.equal(pickup.jobs[0].status, "in_progress"); assert.match(render(pickup), /Picked up/);
    assert.notDeepEqual(pickup.robots.find(r => r.id === "R3"), transferred.robots.find(r => r.id === "R3"));
    await control("ADVANCE"); await control("ADVANCE"); const delivered = await read(); assert.equal(delivered.jobs[0].status, "done"); assert.match(render(delivered), /Delivered/);
  } else { assert.equal(transferred.robots[0].status, "charging"); assert.equal(transferred.stations[0].occupiedBy, "R1"); }
  const duplicate = await decideProposal(tokens.operator, id, "approve", signal()); assert.ok(duplicate.error); assert.equal(duplicate.executionAttempted, false);
  const current = await read(); const stale = await decideProposal(tokens.operator, staleID, "approve", signal()); assert.ok(stale.error); assert.equal(stale.executionAttempted, false); assert.deepEqual(await read(), current);
  await control("EXPIRE"); const expired = await decideProposal(tokens.operator, expiredID, "approve", signal()); assert.equal(expired.state, "expired"); assert.equal(expired.executionAttempted, false); assert.deepEqual(await read(), current);
  console.log(`PASS: dashboard ${reassign ? "reassignment, live pickup/delivery" : "SetCharging"}; real Go/PostgreSQL authenticated reads, typed inbox, approval, rejection, stale/duplicate/expiry/permission boundaries.`);
} finally { lines.close(); }
