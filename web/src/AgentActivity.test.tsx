import { renderToString } from "react-dom/server";
import { expect, it, vi } from "vitest";
import { AgentActivityView, AgentRunDetails, selectionKey, sourceLabels } from "./AgentActivity";
import { activityFixture, agentFixture } from "./agentFixtures";

const clean = (s: string) => s.replace(/<!--.*?-->/g, "");
function render(feed = activityFixture(), selected: string | null = selectionKey(feed.runs[0])) {
  return clean(renderToString(<AgentActivityView feed={feed} selected={selected} select={vi.fn()} refresh={vi.fn()} />));
}
it("shows persisted scripted activity, exact candidate numbers, source, timeline and separate approval stages", () => {
  const html = render();
  for (const text of ["Agent activity", "Scripted/mock agent investigation", "Investigation completed", "Tick 42", "Compare trade-offs", "Traffic blocked", "J1 · R1 → R5", "Go-admitted alternatives", "Required battery", "68 points", "Pickup route steps: 4 &gt; 1", "readIncident", "listRecoveryCandidates", "readRobots", "readJobs", "Sanitized activity summary", "51 ms", "72 ms", "8 total", "provider-reported via agent", "Estimated cost", "Unavailable", "Open human decision inbox", "not approval"]) {
    expect(html).toContain(text);
  }
  expect(html).toContain('href="#approval-inbox"'); expect(html).not.toContain("approve-button"); expect(html).not.toContain("reject-button");
  expect(html).not.toMatch(/Live OpenAI|gpt-4|\$0|Delivery completed|job delivered|provider reasoning:/);
  expect(html).toContain("No command execution recorded"); expect(html).toContain("does not establish delivery completion");
});
it.each(Object.entries(sourceLabels))("faithfully labels persisted %s source", (source, label) => {
  const feed = activityFixture(); feed.runs[0].agentReportedMetadata.reportedSource = source as keyof typeof sourceLabels;
  const html = render(feed); expect(html).toContain(label); expect(html).toContain("Go does not independently attest");
  if (source !== "live-model") expect(html).not.toContain("Agent-reported live-model execution");
});
it("shows missing usage/model/cost as unavailable, and partial reporting without assuming billing", () => {
  const feed = activityFixture(), c = feed.runs[0].agentReportedCompletion!;
  c.reportedUsage = { status: "unavailable", inputTokens: null, outputTokens: null, totalTokens: null }; feed.runs[0].serverElapsedMs = null;
  const html = render(feed); expect(html).not.toContain("0 total"); expect(html).toContain("Server-measured duration</dt><dd>Unavailable");
  expect(html).toContain("Actual model · provider-reported via agent</dt><dd>Unavailable"); expect(html).toContain("no validated pricing estimate");
  c.reportedUsage = { status: "partial", inputTokens: 3, outputTokens: 2, totalTokens: 5 }; expect(render(feed)).toContain("Partial reporting only");
});
it.each([false, null])("shows unconfirmed server verification: %s", verified => {
  const feed = activityFixture(); feed.runs[0].serverVerifiedDecision = verified;
  feed.runs[0].agentReportedCompletion!.decision!.basis = "tradeoff";
  const html = render(feed); expect(html).toContain("Server verification is not confirmed"); expect(html).toContain("Reported numerical comparisons"); expect(html).not.toContain("Go verified the persisted decision");
  expect(html).not.toContain("Verified trade-off");
});
it("shows incomplete-run measurements and cost as unavailable without inventing zero counts", () => {
  const feed = activityFixture(), run = feed.runs[0];
  run.agentReportedCompletion = null; run.serverElapsedMs = null; run.completedAt = null;
  run.proposal = null; run.proposalId = null; run.serverVerifiedDecision = false;
  const html = render(feed);
  for (const label of ["Host-counted model requests", "Host-counted tool executions", "Host-measured investigation latency", "Server-measured duration", "Token usage · provider-reported via agent"]) expect(html).toContain(`${label}</dt><dd>Unavailable`);
  expect(html).toContain("Estimated cost</dt><dd>Unavailable"); expect(html).not.toContain("0 total");
});
it.each(["failed", "rejected", "incomplete", "abstained"])("differentiates %s without inventing a proposal", mode => {
  const feed = activityFixture(), run = feed.runs[0]; run.proposal = null; run.proposalId = null; run.serverSubmissionOutcome = "not_requested"; run.serverVerifiedDecision = false;
  if (mode === "incomplete") { run.agentReportedCompletion = null; run.completedAt = null; run.serverElapsedMs = null; run.serverSubmissionOutcome = "not_completed"; }
  else if (mode === "abstained") { run.agentReportedCompletion!.decision = { kind: "abstain", selected: null, basis: "", reason: "operator_tradeoff", comparisons: [] }; run.serverVerifiedDecision = true; }
  else { run.agentReportedCompletion!.status = mode as "failed" | "rejected"; run.agentReportedCompletion!.failure = mode === "rejected" ? "grounding_rejected" : "timeout"; run.agentReportedCompletion!.decision = null; }
  const html = render(feed); expect(html).toContain(mode === "failed" ? "Failed" : mode === "rejected" ? "Validation rejected" : mode === "incomplete" ? "Incomplete" : "Abstained");
  expect(html).toContain("No associated proposal"); expect(html).not.toContain("pending_proposal_created"); expect(html).not.toContain("command failed");
  expect(html).not.toContain("No validated recommendation</h3><p>Live");
});
it("distinguishes Go creation, human decision, execution and observed delivery", () => {
  const feed = activityFixture(), p = feed.runs[0].proposal!; p.state = "approved"; p.execution = "succeeded";
  let html = render(feed); expect(html).toContain("Go recorded operator approval"); expect(html).toContain("confirms transfer, not pickup or delivery"); expect(html).toContain("job J1 · assigned");
  feed.current!.jobs[0].status = "done"; html = render(feed); expect(html).toContain("simulator reports this job done"); expect(html).toContain("causation by this proposal is not established");
  p.state = "rejected"; p.execution = "none"; expect(render(feed)).toContain("Go recorded operator rejection");
  p.state = "approved"; p.execution = "unknown"; expect(render(feed)).toContain("Execution outcome uncertain");
});
it.each(["forbidden", "unavailable", "malformed", "demo", "paused", "denied"] as const)("renders an honest %s state without retaining private records", phase => {
  const feed = { ...activityFixture(), phase }; const html = render(feed);
  expect(html).not.toContain(agentFixture().id); expect(html).not.toContain("readRobots"); expect(html).not.toContain("J1 · R1 → R5");
  if (phase === "demo") { expect(html).toContain("deterministic local demo"); expect(html).toContain("No live LLM"); }
  if (phase === "forbidden") expect(html).toContain("Viewer credentials cannot access");
});
it("shows a genuine empty response and resets selected details across runtime identities", () => {
  const feed = activityFixture(), old = selectionKey(feed.runs[0]); feed.runs[0].runtimeId = "newRuntime";
  expect(render(feed, old)).not.toContain("Selected agent investigation");
  expect(render({ ...feed, runs: [] }, null)).toContain("No persisted agent runs");
});
it("never renders untrusted HTML as markup", () => {
  const run = agentFixture(); run.id = '<img src=x onerror="bad">';
  const html = renderToString(<AgentRunDetails run={run} current={null} />); expect(html).not.toContain('<img src=x'); expect(html).toContain("&lt;img");
});
