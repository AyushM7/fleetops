import { renderToString } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { pollDemoStatus } from "../restart";
import { openWarehouse, subscribeWarehouse } from "./session";
import { isWarehouseStatus, parseWarehouseFrame, parseWarehouseStatus } from "./telemetry";
import V2Shell from "./V2Shell";

const generation = "a".repeat(64);
const replacement = "b".repeat(64);
const runtimeId = "P" + "1".repeat(32);
const stations = [1, 6, 12, 18].map((x, index) => ({ id: "C" + (index + 1), x, y: 0, occupiedBy: "", reservedBy: "" }));
const layout = {
  width: 20, height: 13,
  cells: Array.from({ length: 260 }, (_, i) => ({
    x: i % 20, y: Math.floor(i / 20),
    kind: i === 9 ? "maintenance_bay" : stations.some(station => station.x === i) ? "station" : i >= 240 ? "parking" : "free",
  })),
};
const robots = Array.from({ length: 11 }, (_, i) => ({
  id: "R" + (i + 1), x: i === 10 ? 9 : i * 2, y: i === 10 ? 0 : 12,
  status: "idle", activity: "PARKED", role: i === 10 ? "maintenance" : "transport",
  battery: 100, jobId: "", blocked: false, waitTicks: 0,
}));
const snapshot = (tick: number) => ({ tick, seed: 7, stations, robots, jobs: [], faults: [], aisles: [] });
const status = (gen = generation, tick = 0) => ({
  generation: gen, scenarioId: "warehouse-v2", ready: true, state: "paused", operationsMode: "deterministic",
  warehouse: snapshot(tick), evidence: { scenarioId: "warehouse-v2", generation: gen, runtimeId, observation: { layout } },
});
const wire = (eventId = 1, tick = 0, gen = generation) => ({
  eventId, tick, runtimeId, generation: gen, state: "paused", operationsMode: "deterministic", snapshot: snapshot(tick),
});
type WireFrame = ReturnType<typeof wire>;
function message<T extends WireFrame>(type: string, frame: T, cursor = frame.generation + ":" + frame.eventId): MessageEvent<string> {
  return new MessageEvent(type, { data: JSON.stringify(frame), lastEventId: cursor });
}
const json = (body: unknown) => new Response(JSON.stringify(body));
const subscriptions: (() => void)[] = [];

class FakeSource extends EventTarget {
  static opened: FakeSource[] = [];
  onerror: (() => void) | null = null;
  close = vi.fn();

  constructor(readonly url: string) {
    super();
    FakeSource.opened.push(this);
  }

  send(type: string, frame: WireFrame): void {
    this.dispatchEvent(message(type, frame));
  }
}

function mockSession() {
  vi.useFakeTimers();
  vi.stubGlobal("EventSource", FakeSource);
  const request = vi.fn<typeof fetch>();
  vi.stubGlobal("fetch", request);
  return request;
}

afterEach(() => {
  subscriptions.splice(0).forEach(stop => stop());
  FakeSource.opened = [];
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("Warehouse V2 wire contracts", () => {
  it("reads the generation-bound layout from status and accepts omitted counters and activity", () => {
    const parsed = parseWarehouseStatus(status());
    if (!parsed.ready) throw new Error("Fixture should be ready");
    expect(parsed.layout.cells).toHaveLength(260);
    expect(parsed.snapshot.robots).toHaveLength(11);
    expect(parsed.snapshot.completedJobs).toBeUndefined();
    expect(parsed.operations).toEqual([]);
    expect(parseWarehouseFrame(message("snapshot", wire()), parsed)).toMatchObject({ eventId: 1, mode: "deterministic" });
  });

  it("rejects malformed payloads, wrong runtime or generation, and inconsistent event cursors", () => {
    const parsed = parseWarehouseStatus(status());
    if (!parsed.ready) throw new Error("Fixture should be ready");
    const invalid = wire();
    invalid.snapshot = { ...invalid.snapshot, robots: robots.map((robot, i) => i === 0 ? { ...robot, battery: 101 } : robot) };
    for (const event of [
      message("snapshot", invalid), message("snapshot", wire(1, 0, replacement)),
      message("snapshot", { ...wire(), runtimeId: "P" + "2".repeat(32) }),
      message("snapshot", wire(), generation + ":2"), message("tick", { ...wire(), tick: 5 }),
      new MessageEvent("snapshot", { data: "none" }),
    ]) expect(() => parseWarehouseFrame(event, parsed)).toThrow();
    expect(() => parseWarehouseStatus({ ...status(), evidence: { ...status().evidence, generation: replacement } })).toThrow();
    expect(() => parseWarehouseStatus({ ...status(), warehouse: undefined, snapshot: snapshot(0) })).toThrow();
  });

  it("validates optional Operations data without treating a proposal as proof of execution", () => {
    const parsed = parseWarehouseStatus(status());
    if (!parsed.ready) throw new Error("Fixture should be ready");
    const proposed = {
      observationId: runtimeId, observedAt: "2026-11-19T00:00:00Z", tick: 0, stage: "proposed",
      status: "proposed", recorded: true, decision: { kind: "assign", assignments: [{ jobId: "J1", robotId: "R1" }] },
      usage: { model: "provider-model", inputTokens: 100, outputTokens: 7 },
    };
    const payload = { ...wire(), operationsActivity: [proposed] };
    expect(parseWarehouseFrame(message("operations", payload), parsed).operations).toEqual([proposed]);
    expect(() => parseWarehouseFrame(message("operations", {
      ...payload, operationsActivity: [{ ...proposed, usage: { ...proposed.usage, inputTokens: -1 } }],
    }), parsed)).toThrow();
  });

  it("posts the fixed launch operation with the expected generation and confirms the replacement", async () => {
    const request = vi.fn<typeof fetch>();
    vi.stubGlobal("fetch", request);
    request.mockResolvedValueOnce(json({ generation: replacement, scenarioId: "warehouse-v2", ready: true }));
    await expect(openWarehouse(generation)).resolves.toBeUndefined();
    expect(request).toHaveBeenCalledWith("/demo/scenario/warehouse-v2", {
      method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error", signal: expect.any(AbortSignal),
      body: "{}", headers: { "Content-Type": "application/json", "X-FleetOps-Demo": "1", "X-FleetOps-Generation": generation },
    });
    request.mockResolvedValueOnce(json({ generation, scenarioId: "warehouse-v2", ready: true }));
    await expect(openWarehouse(generation)).rejects.toThrow("not confirmed");
    request.mockResolvedValueOnce(new Response("", { status: 409 }));
    await expect(openWarehouse(generation)).rejects.toThrow("not confirmed");
    await expect(openWarehouse("invalid")).rejects.toThrow("Refresh");
    expect(request).toHaveBeenCalledTimes(3);
  });

  it("routes confirmed V2 status before the existing V1 parser runs", async () => {
    const request = mockSession();
    request.mockResolvedValueOnce(json(status()));
    const emit = vi.fn();
    const onV2 = vi.fn(isWarehouseStatus);
    subscriptions.push(pollDemoStatus(emit, vi.fn(), 250, onV2));
    await vi.advanceTimersByTimeAsync(500);
    expect(onV2).toHaveBeenCalledOnce();
    expect(emit).not.toHaveBeenCalled();
    expect(request).toHaveBeenCalledOnce();
  });
});

describe("Warehouse V2 stream lifecycle", () => {
  it("waits for a snapshot, ignores duplicate frames, and resynchronizes before accepting more ticks", async () => {
    const request = mockSession();
    request.mockResolvedValueOnce(json(status())).mockResolvedValueOnce(json(status(generation, 4)));
    const emit = vi.fn();
    subscriptions.push(subscribeWarehouse(emit, vi.fn()));
    await vi.advanceTimersByTimeAsync(0);
    expect(request).toHaveBeenCalledWith("/demo/status", expect.objectContaining({ method: "POST", credentials: "same-origin", body: "{}" }));
    const [first] = FakeSource.opened;
    expect(first.url).toBe("/demo/events");
    first.send("tick", wire(2, 1));
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "connecting", frame: null }));
    first.send("snapshot", wire());
    first.send("tick", wire(2, 1));
    first.send("state", wire(3, 1));
    first.send("tick", wire(2, 1));
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "live", frame: expect.objectContaining({ eventId: 3 }) }));
    first.send("tick", wire(6, 4));
    expect(first.close).toHaveBeenCalledOnce();
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "reconnecting", frame: expect.objectContaining({ eventId: 3 }) }));
    first.send("tick", wire(4, 2));
    await vi.advanceTimersByTimeAsync(2000);

    const second = FakeSource.opened[1];
    second.send("snapshot", wire(3, 1));
    second.send("tick", wire(6, 4));
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "reconnecting", frame: expect.objectContaining({ eventId: 3 }) }));
    second.send("snapshot", wire(6, 4));
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "live", frame: expect.objectContaining({ eventId: 6 }) }));
  });

  it("rechecks a silent running stream while allowing a paused stream to stay idle", async () => {
    const request = mockSession();
    request.mockResolvedValueOnce(json(status()));
    const emit = vi.fn();
    subscriptions.push(subscribeWarehouse(emit, vi.fn()));
    await vi.advanceTimersByTimeAsync(0);
    const [source] = FakeSource.opened;
    source.send("snapshot", wire());
    await vi.advanceTimersByTimeAsync(15000);
    expect(source.close).not.toHaveBeenCalled();
    source.send("state", { ...wire(2), state: "running" });
    await vi.advanceTimersByTimeAsync(7000);
    source.send("tick", { ...wire(3, 1), state: "running" });
    await vi.advanceTimersByTimeAsync(7999);
    expect(source.close).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(source.close).toHaveBeenCalledOnce();
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "reconnecting", frame: expect.objectContaining({ eventId: 3 }) }));
  });

  it("discards previous generations and stops reconnecting when the visitor expires", async () => {
    const request = mockSession();
    request.mockResolvedValueOnce(json(status())).mockResolvedValueOnce(json(status(replacement)))
      .mockResolvedValueOnce(new Response("", { status: 401 }));
    const emit = vi.fn();
    subscriptions.push(subscribeWarehouse(emit, vi.fn()));
    await vi.advanceTimersByTimeAsync(0);
    FakeSource.opened[0].send("snapshot", wire());
    FakeSource.opened[0].onerror?.();
    await vi.advanceTimersByTimeAsync(2000);
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "connecting", frame: null }));
    FakeSource.opened[1].send("snapshot", wire());
    expect(FakeSource.opened[1].close).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(10000);
    expect(emit).toHaveBeenLastCalledWith({ phase: "expired", frame: null, status: null });
    expect(request).toHaveBeenCalledTimes(3);
  });

  it("returns to V1 after a status read confirms a legacy scenario", async () => {
    const request = mockSession();
    request.mockResolvedValueOnce(json({ generation, scenarioId: "routine-charging", ready: true }));
    const onLegacy = vi.fn();
    subscriptions.push(subscribeWarehouse(vi.fn(), onLegacy));
    await vi.advanceTimersByTimeAsync(10000);
    expect(onLegacy).toHaveBeenCalledOnce();
    expect(request).toHaveBeenCalledOnce();
    expect(FakeSource.opened).toHaveLength(0);
  });

  it("closes the active stream on cleanup and ignores late deliveries", async () => {
    const request = mockSession();
    request.mockResolvedValueOnce(json(status()));
    const emit = vi.fn();
    const stop = subscribeWarehouse(emit, vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    stop();
    FakeSource.opened[0].send("snapshot", wire());
    FakeSource.opened[0].onerror?.();
    await vi.advanceTimersByTimeAsync(10000);
    expect(FakeSource.opened[0].close).toHaveBeenCalledOnce();
    expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "connecting", frame: null }));
    expect(request).toHaveBeenCalledOnce();
  });

  it("renders the V2 pages without the legacy proposal UI or synthetic telemetry", () => {
    const html = renderToString(<V2Shell onLegacy={vi.fn()} />);
    for (const label of ["Warehouse V2", "Fleet", "Jobs", "Operations AI", "Connecting", 'aria-current="page"']) expect(html).toContain(label);
    expect(html).not.toContain("Approval inbox");
    expect(html).not.toContain("Simulation tick");
  });
});
