import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchFleet, FleetError, parseSnapshot, pollFleet, type Feed, type FleetSnapshot } from "./fleet";

// Protocol fixtures are used only in tests; the application has no simulated feed.
export function fixture(): FleetSnapshot {
  return { tick: 7, poisoned: false, activeIncidents: [], jobs: [], layout: { width: 4, height: 4, cells: Array.from({ length: 16 }, (_, i) => ({ x: i % 4, y: Math.floor(i / 4), kind: i === 0 ? "station" : i % 4 === 1 ? "aisle" : "free" })) },
    robots: [{ id: "R1", x: 0, y: 0, battery: 70, status: "idle", jobId: "", blocked: false, waitTicks: 0 }],
    stations: [{ id: "S1", x: 0, y: 0, occupiedBy: "", reservedBy: "" }] };
}
const response = (snapshot = fixture()) => new Response(JSON.stringify({ data: { fleetState: snapshot } }));
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("fleet transport", () => {
  it("uses a read-only query and an in-memory bearer header without cookies or redirects", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(response());
    const signal = new AbortController().signal;
    expect(await fetchFleet("test-only-token", signal, request)).toEqual(fixture());
    const [url, options] = request.mock.calls[0];
    expect(url).toBe("/graphql");
    expect(options).toMatchObject({ method: "POST", credentials: "omit", redirect: "error", cache: "no-store", signal });
    expect(options?.headers).toMatchObject({ Authorization: "Bearer test-only-token" });
    expect(options?.body).not.toContain("mutation");
    expect(options?.body).not.toContain("test-only-token");
  });
  it("distinguishes authentication from backend and schema failures", async () => {
    for (const status of [401, 403]) {
      const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status }));
      await expect(fetchFleet("token", new AbortController().signal, request)).rejects.toMatchObject({ kind: "authentication" });
    }
    for (const result of [new Response(null, { status: 503 }), new Response('{"errors":[{"message":"private error"}]}'), new Response("not json")]) {
      const request = vi.fn<typeof fetch>().mockResolvedValue(result);
      await expect(fetchFleet("token", new AbortController().signal, request)).rejects.toThrow();
    }
  });
  it("refuses inaccurate or unsupported layout and robot data", () => {
    const cases = [() => ({ ...fixture(), layout: { ...fixture().layout, width: 0 } }), () => ({ ...fixture(), layout: { ...fixture().layout, cells: [] } }),
      () => ({ ...fixture(), robots: [{ ...fixture().robots[0], x: 99 }] }), () => ({ ...fixture(), robots: [{ ...fixture().robots[0], status: "invented" }] }),
      () => ({ ...fixture(), robots: [{ ...fixture().robots[0], battery: 101 }] })];
    for (const invalid of cases) expect(() => parseSnapshot(invalid())).toThrow(FleetError);
    const repeated = fixture(); repeated.layout.cells[1] = repeated.layout.cells[0];
    expect(() => parseSnapshot(repeated)).toThrow(FleetError);
  });
});

describe("polling lifecycle", () => {
  it("refreshes ticks, retains an explicitly stale snapshot on disconnection, and recovers", async () => {
    vi.useFakeTimers();
    const newer = { ...fixture(), tick: 9 };
    const request = vi.fn<typeof fetch>().mockResolvedValueOnce(response()).mockRejectedValueOnce(new TypeError("network down")).mockResolvedValue(response(newer));
    vi.stubGlobal("fetch", request);
    const feeds: Feed[] = [];
    const stop = pollFleet("token", feed => feeds.push(feed), vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    expect(feeds.at(-1)?.snapshot?.tick).toBe(7);
    await vi.advanceTimersByTimeAsync(1000);
    expect(feeds.at(-1)).toMatchObject({ phase: "unavailable", snapshot: { tick: 7 } });
    await vi.advanceTimersByTimeAsync(1000);
    expect(feeds.at(-1)).toMatchObject({ phase: "live", snapshot: { tick: 9 } });
    stop(); const count = request.mock.calls.length;
    await vi.advanceTimersByTimeAsync(2000);
    expect(request).toHaveBeenCalledTimes(count);
  });
  it("stops retrying after authentication fails", async () => {
    vi.useFakeTimers();
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 401 }));
    vi.stubGlobal("fetch", request);
    const denied = vi.fn(); const stop = pollFleet("invalid", vi.fn(), denied);
    await vi.advanceTimersByTimeAsync(5000);
    expect(denied).toHaveBeenCalledOnce(); expect(request).toHaveBeenCalledOnce(); stop();
  });
  it("aborts on disconnect and ignores a late response", async () => {
    vi.useFakeTimers();
    let complete!: (value: Response) => void;
    const request = vi.fn<typeof fetch>().mockImplementation(() => new Promise(resolve => { complete = resolve; }));
    vi.stubGlobal("fetch", request); const emit = vi.fn();
    const stop = pollFleet("token", emit, vi.fn());
    const signal = request.mock.calls[0][1]?.signal;
    await vi.advanceTimersByTimeAsync(2000); expect(request).toHaveBeenCalledOnce();
    stop(); expect(signal?.aborted).toBe(true); complete(response());
    await vi.advanceTimersByTimeAsync(0);
    expect(emit).toHaveBeenCalledOnce();
  });
  it("times out a stalled request and retries", async () => {
    vi.useFakeTimers();
    const request = vi.fn<typeof fetch>().mockImplementation((_url, options) => new Promise((_resolve, reject) => {
      options?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    }));
    vi.stubGlobal("fetch", request); const emit = vi.fn();
    const stop = pollFleet("token", emit, vi.fn());
    await vi.advanceTimersByTimeAsync(8000);
    expect(emit.mock.calls.at(-1)?.[0].phase).toBe("unavailable");
    await vi.advanceTimersByTimeAsync(1000); expect(request).toHaveBeenCalledTimes(2); stop();
  });
});
