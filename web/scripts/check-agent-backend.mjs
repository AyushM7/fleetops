import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { writeFileSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { renderToString } from "react-dom/server";
import { fetchAgentRuns, AgentActivityView, selectionKey, fetchFleet, fetchPending, decideProposal, Dashboard, InboxView } from "../dist/contract/contract-entry.js";

const endpoint = process.env.FLEETOPS_P404_URL, tokens = JSON.parse(process.env.FLEETOPS_P404_TOKENS);
assert.equal(new URL(endpoint).hostname, "127.0.0.1");
// Use AGENT-05's actual scripted Mastra tool loop to create the real PostgreSQL record.
execFileSync(process.execPath, [fileURLToPath(new URL("../../agent/scripts/check-agent-run-fixture.mjs", import.meta.url))], {
  env: { ...process.env, FLEETOPS_AGENT_FIXTURE_URL: endpoint, FLEETOPS_AGENT_FIXTURE_TOKEN: tokens.proposer }, stdio: "pipe",
});
const network = globalThis.fetch;
globalThis.fetch = (path, options) => { assert.equal(path, "/graphql"); return network(new URL(path, endpoint), options); };
const signal = () => new AbortController().signal;
await assert.rejects(fetchAgentRuns(tokens.viewer, signal()), { kind: "forbidden" });
const read = () => fetchAgentRuns(tokens.operator, signal());
let activity = await read(); assert.equal(activity.runs.length, 1);
let run = activity.runs[0]; assert.equal(run.agentReportedMetadata.reportedSource, "mocked"); assert.equal(run.serverVerifiedDecision, true);
const owned = await fetchAgentRuns(tokens.proposer, signal()); assert.equal(owned.runs.length, 1); assert.equal(owned.runs[0].id, run.id);
assert.equal(run.agentReportedCompletion.modelRequests, 4); assert.equal(run.agentReportedCompletion.toolExecutions, 6);
const feed = () => ({ phase: "ready", ...activity, updatedAt: new Date() });
const agentPanel = () => createElement(AgentActivityView, { feed: feed(), selected: selectionKey(run), select() {}, refresh() {} });
let html = renderToString(agentPanel());
for (const text of ["Scripted/mock agent investigation", "Verified numerical comparisons", "readIncident", "readJobs", "No command execution recorded", "Awaiting an independent operator"]) assert.ok(html.includes(text), text);
assert.ok(!html.includes("Live OpenAI"));
const before = await fetchFleet(tokens.viewer, signal()), proposals = await fetchPending(tokens.operator, signal());
assert.equal(proposals.length, 1); assert.equal(proposals[0].id, run.proposalId);
const inbox = createElement(InboxView, { feed: { phase: "ready", proposals, updatedAt: new Date() }, busy: null, receipt: null, runtimeAvailable: true, decide() {}, refresh() {}, refreshStatus() {} });
if (process.env.FLEETOPS_AGENT06_HTML) {
  const body = renderToString(createElement(Dashboard, { feed: { phase: "live", snapshot: before, updatedAt: new Date() }, decisionPanel: inbox, agentActivity: agentPanel() }));
  const css = readFileSync(new URL("../src/styles.css", import.meta.url), "utf8");
  writeFileSync(process.env.FLEETOPS_AGENT06_HTML, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>FleetOps — persisted scripted investigation</title><style>${css}</style></head><body><div class="app-shell"><header class="topbar"><span class="brand">FleetOps</span><span>Persisted scripted-agent fixture · no paid inference</span></header><main>${body}</main></div></body></html>`);
}
for (const role of ["proposer", "self"]) { const denied = await decideProposal(tokens[role], run.proposalId, "approve", signal()); assert.ok(denied.error); assert.equal(denied.executionAttempted, false); }
assert.deepEqual(await fetchFleet(tokens.viewer, signal()), before);
const approved = await decideProposal(tokens.operator, run.proposalId, "approve", signal()); assert.equal(approved.execution, "succeeded");
activity = await read(); run = activity.runs[0]; html = renderToString(agentPanel());
assert.ok(html.includes("Go recorded operator approval")); assert.ok(html.includes("confirms transfer, not pickup or delivery"));
assert.ok(!html.includes("simulator reports this job done"));
assert.equal(activity.current.jobs.find(j => j.id === "J1").status, "assigned");
console.log("PASS: actual Mastra scripted investigation -> PostgreSQL provenance -> authenticated frontend parsing -> rendered evidence/timeline -> independent approval -> Go receipt; zero paid inference.");
