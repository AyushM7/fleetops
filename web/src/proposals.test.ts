import { afterEach, describe, expect, it, vi } from "vitest";
import { decideProposal, fetchPending, fetchProposal, pollPending, type DecisionResult, type InboxFeed, type Proposal } from "./proposals";
const proposal = (): Proposal => ({ id: "P-test", action: "set_charging", robotId: "R1", stationId: "S1", state: "pending", execution: "none", submittedBy: "proposer", currentRuntime: true, createdAt: "2026-10-16T12:00:00Z", expiresAt: "2030-10-16T12:00:00Z" });
const response = (pending = [proposal()]) => new Response(JSON.stringify({ data: { pendingProposals: pending } }));
const signal = () => new AbortController().signal;
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("proposal protocol", () => {
  it("uses authenticated read operations without identity inputs, cookies or credentials in the body", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(response()); vi.stubGlobal("fetch", request);
    expect(await fetchPending("private-test-value", signal())).toEqual([proposal()]);
    expect(request.mock.calls[0][0]).toBe("/graphql");
    expect(request.mock.calls[0][1]).toMatchObject({ credentials: "omit", cache: "no-store", redirect: "error", headers: { Authorization: "Bearer private-test-value" } });
    expect(request.mock.calls[0][1]?.body).not.toContain("private-test-value");
    expect(request.mock.calls[0][1]?.body).not.toContain("mutation");
  });
  it("distinguishes role denial from authentication and backend failures", async () => {
    const request = vi.fn<typeof fetch>(); vi.stubGlobal("fetch", request);
    request.mockResolvedValueOnce(new Response(null, { status: 401 }));
    await expect(fetchPending("token", signal())).rejects.toMatchObject({ kind: "authentication" });
    request.mockResolvedValueOnce(new Response('{"errors":[{"message":"operator access required","extensions":{"code":"FORBIDDEN"}}],"data":null}'));
    await expect(fetchPending("token", signal())).rejects.toMatchObject({ kind: "forbidden" });
    request.mockResolvedValueOnce(new Response(null, { status: 503 }));
    await expect(fetchPending("token", signal())).rejects.toMatchObject({ kind: "unavailable" });
  });
  it("rejects oversized, non-pending, foreign-runtime and malformed lists", async () => {
    const request = vi.fn<typeof fetch>(); vi.stubGlobal("fetch", request);
    for (const data of [Array.from({ length: 51 }, proposal), [{ ...proposal(), state: "approved" }], [{ ...proposal(), currentRuntime: false }], [{ ...proposal(), expiresAt: "invalid" }]]) {
      request.mockResolvedValueOnce(new Response(JSON.stringify({ data: { pendingProposals: data } })));
      await expect(fetchPending("token", signal())).rejects.toMatchObject({ kind: "unavailable" });
    }
  });
  it("preserves duplicate, stale and uncertain decision responses without claiming success", async () => {
    const request = vi.fn<typeof fetch>(); vi.stubGlobal("fetch", request);
    for (const result of [
      { proposalId: "P-test", state: "approved", execution: "succeeded", auditCommitted: true, executionAttempted: false, error: "proposal already decided" },
      { proposalId: "P-test", state: "expired", execution: "none", auditCommitted: true, executionAttempted: false, error: "proposal expired" },
      { proposalId: "P-test", state: "", execution: "unknown", auditCommitted: false, executionAttempted: true, error: "workflow unavailable; reconciliation may be required" },
    ] satisfies DecisionResult[]) {
      request.mockResolvedValueOnce(new Response(JSON.stringify({ data: { approveProposal: result } })));
      expect(await decideProposal("token", "P-test", "approve", signal())).toEqual(result);
    }
    const body = JSON.parse(String(request.mock.calls[0][1]?.body));
    expect(body.variables).toEqual({ id: "P-test" }); expect(body.query).toContain("approveProposal"); expect(body.query).not.toContain("operator");
  });
  it("sends rejection through the existing mutation and reads canonical status", async () => {
    const result = { proposalId: "P-test", state: "rejected", execution: "none", auditCommitted: true, executionAttempted: false, error: null };
    const request = vi.fn<typeof fetch>().mockResolvedValueOnce(new Response(JSON.stringify({ data: { rejectProposal: result } }))).mockResolvedValueOnce(new Response(JSON.stringify({ data: { proposal: { ...proposal(), state: "rejected" } } }))); vi.stubGlobal("fetch", request);
    expect(await decideProposal("token", "P-test", "reject", signal())).toEqual(result);
    expect(request.mock.calls[0][1]?.body).toContain("rejectProposal");
    expect((await fetchProposal("token", "P-test", signal()))?.state).toBe("rejected");
  });
  it("does not automatically retry a decision after losing its response", async () => {
    vi.useFakeTimers(); const request = vi.fn<typeof fetch>().mockRejectedValue(new TypeError("network lost")); vi.stubGlobal("fetch", request);
    await expect(decideProposal("token", "P-test", "approve", signal())).rejects.toThrow();
    await vi.advanceTimersByTimeAsync(30000); expect(request).toHaveBeenCalledOnce();
  });
});

describe("inbox polling", () => {
  it("removes only confirmed closed outcomes and does not restore them during read failure", async () => {
    vi.useFakeTimers(); const request = vi.fn<typeof fetch>().mockResolvedValueOnce(response()).mockRejectedValue(new TypeError("offline")); vi.stubGlobal("fetch", request);
    const feeds: InboxFeed[] = []; const poll = pollPending("token", feed => feeds.push(feed), vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    const result: DecisionResult = { proposalId: "P-test", state: "approved", execution: "unknown", auditCommitted: false, executionAttempted: true, error: "workflow unavailable" };
    poll.settled(result); expect(feeds.at(-1)?.proposals).toHaveLength(1);
    poll.settled({ ...result, auditCommitted: true, execution: "succeeded", error: "proposal already decided" });
    expect(feeds.at(-1)?.proposals).toEqual([]);
    poll.refresh(); await vi.advanceTimersByTimeAsync(0);
    expect(feeds.at(-1)).toMatchObject({ phase: "unavailable", proposals: [] }); poll.stop();
  });
  it("retains an explicitly stale list during disconnection and recovers", async () => {
    vi.useFakeTimers(); const request = vi.fn<typeof fetch>().mockResolvedValueOnce(response()).mockRejectedValueOnce(new TypeError("offline")).mockResolvedValue(response([])); vi.stubGlobal("fetch", request);
    const feeds: InboxFeed[] = []; const poll = pollPending("token", feed => feeds.push(feed), vi.fn());
    await vi.advanceTimersByTimeAsync(0); expect(feeds.at(-1)?.phase).toBe("ready");
    await vi.advanceTimersByTimeAsync(2000); expect(feeds.at(-1)).toMatchObject({ phase: "unavailable", proposals: [{ id: "P-test" }] });
    await vi.advanceTimersByTimeAsync(2000); expect(feeds.at(-1)).toMatchObject({ phase: "ready", proposals: [] }); poll.stop();
  });
  it("does not poll forbidden or unauthenticated inboxes repeatedly", async () => {
    vi.useFakeTimers();
    for (const forbidden of [true, false]) {
      const request = vi.fn<typeof fetch>().mockImplementation(async () => forbidden ? new Response('{"errors":[{"extensions":{"code":"FORBIDDEN"}}]}') : new Response(null, { status: 401 })); vi.stubGlobal("fetch", request);
      const emit = vi.fn(); const denied = vi.fn(); const poll = pollPending("token", emit, denied);
      await vi.advanceTimersByTimeAsync(10000); expect(request).toHaveBeenCalledOnce();
      if (forbidden) expect(emit.mock.calls.at(-1)?.[0].phase).toBe("forbidden"); else expect(denied).toHaveBeenCalledOnce(); poll.stop();
    }
  });
  it("manual refresh discards old reads so decided proposals cannot reappear", async () => {
    vi.useFakeTimers(); let complete!: (value: Response) => void;
    const request = vi.fn<typeof fetch>().mockImplementationOnce(() => new Promise(resolve => { complete = resolve; })).mockResolvedValue(response([])); vi.stubGlobal("fetch", request);
    const emit = vi.fn(); const poll = pollPending("token", emit, vi.fn());
    poll.refresh(); await vi.advanceTimersByTimeAsync(0);
    complete(response()); await vi.advanceTimersByTimeAsync(0);
    expect(emit.mock.calls.at(-1)?.[0].proposals).toEqual([]);
    poll.stop(); await vi.advanceTimersByTimeAsync(5000); expect(request).toHaveBeenCalledTimes(2);
  });
});
