import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToString } from "react-dom/server";
import SimulationControls from "./SimulationControls";
import { controlSimulation, pollDemoStatus, type DemoStatus } from "./restart";

const generation = "a".repeat(64);
const snapshot = { tick: 42, poisoned: false, layout: { width: 1, height: 1, cells: [{ x: 0, y: 0, kind: "free" as const }] }, robots: [], stations: [], jobs: [], activeIncidents: [] };
const status: DemoStatus = { generation, ready: true, state: "paused", speed: "1", snapshot };
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("simulation controls", () => {
  it("gates pending decisions, running Steps, unknown status, loading and errors accessibly", () => {
    const render = (value: DemoStatus | null, busy = false, unavailable = false, error: string | null = null) => renderToString(<SimulationControls status={value} busy={busy} unavailable={unavailable} error={error} onControl={() => {}} />);
    const pending = render({ ...status, state: "awaiting_decision" });
    expect(pending).toContain("Awaiting Decision"); expect(pending).toContain("human decision");
    expect(pending.match(/disabled=""/g)).toHaveLength(2);
    const running = render({ ...status, state: "running" });
    expect(running).toContain(">Pause</button>"); expect(running.match(/disabled=""/g)).toHaveLength(1);
    expect(render(status)).not.toContain("disabled");
    expect(render(status, true)).toContain("Applying control"); expect(render(status, true)).toContain('aria-busy="true"');
    expect(render(null).match(/disabled=""/g)).toHaveLength(3);
    expect(render(status, false, true, "Outcome unknown")).toContain('role="alert"');
    expect(render(status, false, true)).toContain("Unavailable");
    expect(render(status)).toContain('aria-label="Simulation speed"');
  });
  it("sends fixed authenticated controls with generation and observed tick, without retrying", async () => {
    const request = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...status, snapshot: { ...snapshot, tick: 43 } })));
    vi.stubGlobal("fetch", request);
    expect((await controlSimulation("step", generation, 42)).snapshot?.tick).toBe(43);
    const [url, options] = request.mock.calls[0];
    expect(url).toBe("/demo/step"); expect(options.body).toBe("{}");
    expect(options.headers["X-FleetOps-Tick"]).toBe("42"); expect(options.headers["X-FleetOps-Generation"]).toBe(generation);
    expect(options.headers.Authorization).toBeUndefined(); expect(options.credentials).toBe("same-origin");
    request.mockRejectedValue(new Error("connection lost"));
    await expect(controlSimulation("step", generation, 43)).rejects.toThrow();
    expect(request).toHaveBeenCalledTimes(2);
  });
  it("rejects stale, poisoned, invalid rate and malformed acknowledgements", async () => {
    for (const body of [{ ...status, generation: "b".repeat(64) }, { ...status, speed: "3" }, { ...status, state: "ended" }, { ...status, snapshot: { ...snapshot, poisoned: true } }, { ...status, snapshot: undefined }]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
      await expect(controlSimulation("play", generation, 42)).rejects.toThrow();
    }
    for (const code of [401, 403, 409, 503]) {
      const request = vi.fn().mockResolvedValue(new Response("denied", { status: code })); vi.stubGlobal("fetch", request);
      await expect(controlSimulation("step", generation, 42)).rejects.toThrow(); expect(request).toHaveBeenCalledOnce();
    }
  });
  it("samples real snapshots at bounded cadence with one in-flight read and aborts on cleanup", async () => {
    vi.useFakeTimers();
    let resolve: ((value: Response) => void) | undefined;
    const request = vi.fn().mockImplementation(() => new Promise<Response>(done => { resolve = done; }));
    vi.stubGlobal("fetch", request); const emit = vi.fn();
    const stop = pollDemoStatus(emit, vi.fn(), 250);
    await vi.advanceTimersByTimeAsync(1000); expect(request).toHaveBeenCalledOnce();
    resolve?.(new Response(JSON.stringify(status))); await vi.advanceTimersByTimeAsync(0);
    expect(emit.mock.calls[0][0].snapshot.tick).toBe(42);
    await vi.advanceTimersByTimeAsync(250); expect(request).toHaveBeenCalledTimes(2);
    stop(); expect(request.mock.calls[1][1].signal.aborted).toBe(true);
    resolve?.(new Response(JSON.stringify({ ...status, snapshot: { ...snapshot, tick: 43 } })));
    await vi.advanceTimersByTimeAsync(2000); expect(emit).toHaveBeenCalledOnce();
  });
});
