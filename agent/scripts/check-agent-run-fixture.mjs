import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";
import { createFleetClient, runRecoveryDecision } from "../dist/index.js";
import { decisionLimitations } from "../dist/decision-contracts.js";

const endpoint = process.env.FLEETOPS_AGENT_FIXTURE_URL;
const token = process.env.FLEETOPS_AGENT_FIXTURE_TOKEN;
const network = globalThis.fetch;
globalThis.fetch = (url, init) => { assert.equal(new URL(String(url)).href, new URL(endpoint).href); return network(url, init); };
const api = createFleetClient(endpoint, token);
let calls = 0;
let summary;
function evidence(prompt) {
  return prompt.filter(m => m.role === "tool").flatMap(m => m.content)
    .filter(p => p.type === "tool-result").map(p => p.output.value);
}
function key(candidate) { const { jobId, fromRobotId, toRobotId } = candidate; return { jobId, fromRobotId, toRobotId }; }
function decision(results) {
  const incidents = results.filter(r => r.incident).map(r => r.incident);
  const candidates = results.flatMap(r => r.candidates ?? []);
  const robots = results.flatMap(r => r.robots ?? []), jobs = results.flatMap(r => r.jobs ?? []);
  const considered = candidates.map(candidate => {
    const recipient = robots.find(r => r.id === candidate.toRobotId);
    return { candidate, source: robots.find(r => r.id === candidate.fromRobotId), recipient, job: jobs.find(j => j.id === candidate.jobId),
      incidentReference: summary.incidents.find(i => i.reference.robotId === candidate.fromRobotId && i.reference.type === "traffic_blocked").reference,
      totalRouteSteps: candidate.pickupSteps + candidate.deliverySteps + candidate.chargingExitSteps,
      missionHeadroom: recipient.battery - candidate.requiredBattery, admissionHeadroom: recipient.battery - Math.max(30, candidate.requiredBattery) };
  });
  assert.ok(considered.length >= 2);
  const selected = considered.at(-1);
  return { version: "recovery-decision-v2", observationId: summary.observationId, tick: summary.tick, objective: summary.objective,
    incidents: incidents.map(i => ({ reference: summary.incidents.find(row => row.reference.robotId === i.robotId && row.reference.type === i.type).reference, severity: i.severity, telemetry: i.telemetry })),
    considered, comparisons: considered.filter(row => row !== selected).map(row => ({ left: key(selected.candidate), right: key(row.candidate), metric: "pickupSteps",
      leftValue: selected.candidate.pickupSteps, rightValue: row.candidate.pickupSteps,
      relation: selected.candidate.pickupSteps < row.candidate.pickupSteps ? "less" : selected.candidate.pickupSteps > row.candidate.pickupSteps ? "greater" : "equal" })),
    decision: { kind: "select", selected: key(selected.candidate), basis: "admitted_option" },
    operatorAttention: summary.incidents.map(i => i.reference), limitations: [...decisionLimitations] };
}
const model = { specificationVersion: "v2", provider: "offline-agent05", modelId: "scripted", supportedUrls: {},
  async doGenerate(options) {
    const index = calls++;
    let content;
    if (index === 0) {
      summary = JSON.parse(options.prompt.find(m => m.role === "user").content[0].text);
      assert.equal(summary.reassignmentCandidates, undefined);
      content = summary.incidents.flatMap(i => ["readIncident", "listRecoveryCandidates"].map(toolName => ({ toolName, args: { reference: i.reference } })));
    } else if (index === 1) {
      const results = evidence(options.prompt), candidates = results.flatMap(r => r.candidates ?? []);
      content = [{ toolName: "readRobots", args: { ids: [...new Set(candidates.flatMap(c => [c.fromRobotId, c.toRobotId]))] } },
        { toolName: "readJobs", args: { ids: [...new Set(candidates.map(c => c.jobId))] } }];
    } else if (index === 2) content = "Evidence ready.";
    else { assert.equal(index, 3); assert.equal(options.toolChoice.type, "none"); content = JSON.stringify(decision(evidence(options.prompt))); }
    return { content: typeof content === "string" ? [{ type: "text", text: content }] : content.map((call, id) => ({ type: "tool-call", toolCallId: `${index}-${id}`, toolName: call.toolName, input: JSON.stringify(call.args) })),
      finishReason: typeof content === "string" ? "stop" : "tool-calls", usage: { inputTokens: 1, outputTokens: 1, totalTokens: 2 }, warnings: [] };
  }, doStream() { throw new Error("Unexpected streaming method"); } };
const result = await runRecoveryDecision(api, { enabled: true, model, source: "mocked", objective: "compare_tradeoffs", allowSubmit: true });
assert.equal(result.aiAnalysis.status, "accepted"); assert.equal(result.provenance.status, "recorded");
assert.equal(result.proposal.status, "pending_approval"); assert.equal(calls, 4);
const response = await fetch(endpoint, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
  body: JSON.stringify({ query: `query($id:ID!){agentRun(id:$id){data proposal{id state execution}}}`, variables: { id: result.provenance.runId } }) });
const body = await response.json(); assert.equal(body.errors, undefined);
const stored = body.data.agentRun, record = JSON.parse(stored.data);
assert.equal(stored.proposal.id, result.proposal.proposalId); assert.equal(stored.proposal.state, "pending"); assert.equal(stored.proposal.execution, "none");
assert.equal(record.serverVerifiedDecision, true); assert.equal(record.agentReportedMetadata.reportedSource, "mocked");
assert.equal(record.agentReportedCompletion.modelRequests, 4); assert.equal(record.agentReportedCompletion.toolExecutions, 6);
assert.equal(record.agentReportedCompletion.reportedUsage.totalTokens, 8); assert.equal(record.estimatedCostStatus, "unavailable");
assert.equal(record.agentReportedCompletion.steps.length, 10);
assert.notEqual(result.recommendedAction.toRobotId, record.serverVerifiedObservation.candidates[0].key.toRobotId);
if (process.env.FLEETOPS_AGENT_RECORD_PATH) writeFileSync(process.env.FLEETOPS_AGENT_RECORD_PATH, JSON.stringify({ ...record, proposal: stored.proposal }, null, 2));
console.log("PASS: actual Mastra tools -> non-first validated decision -> atomic durable run/proposal -> authenticated retrieval; zero paid requests.");
