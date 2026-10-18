import { afterEach, expect, it, vi } from "vitest";
import { renderToString } from "react-dom/server";
import { ScenarioSelector, scenarios, type ScenarioID } from "./scenarios";
import { selectScenario, pollDemoStatus } from "./restart";
import SimulationControls from "./SimulationControls";

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });
const generation = "a".repeat(64), next = "b".repeat(64);

it("shows four accessible choices with truthful scenario context, loading, and progress", () => {
  for (const scenario of scenarios) {
    const html = renderToString(<ScenarioSelector selected={scenario.id} busy={false} disabled={false} onSelect={() => {}} />);
    expect(html).toContain('aria-label="Warehouse scenario"');
    expect(html).toContain('aria-describedby="scenario-description"');
    expect(html.match(/<option/g)).toHaveLength(4);
    expect(html).toContain(scenario.description);
  }
  expect(renderToString(<ScenarioSelector busy={false} disabled onSelect={() => {}} />)).toContain("Loading scenarios");
  const busy = renderToString(<ScenarioSelector selected="routine-charging" busy disabled={false} onSelect={() => {}} />);
  expect(busy).toContain('aria-busy="true"'); expect(busy).toContain("disabled"); expect(busy).toContain("preserving evidence");
});

it("selects only a fixed allowlist operation, requiring fresh identity and matching scenario", async () => {
  for (const scenario of scenarios) {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ generation: next, ready: true, scenarioId: scenario.id })));
    vi.stubGlobal("fetch", fetcher);
    expect((await selectScenario(scenario.id, generation)).scenarioId).toBe(scenario.id);
    const [url, options] = fetcher.mock.calls[0];
    expect(url).toBe(`/demo/scenario/${scenario.id}`); expect(options.body).toBe("{}");
    expect(options.headers["X-FleetOps-Generation"]).toBe(generation); expect(options.credentials).toBe("same-origin");
  }
  const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
  await expect(selectScenario("../arbitrary" as ScenarioID, generation)).rejects.toThrow("Unsupported");
  expect(fetcher).not.toHaveBeenCalled();
  for (const response of [new Response("uncertain", { status: 503 }), new Response("stale", { status: 409 }), new Response(JSON.stringify({ generation, ready: true, scenarioId: "routine-charging" })), new Response(JSON.stringify({ generation: next, ready: true, scenarioId: "healthy-operations" }))]) {
    const request = vi.fn().mockResolvedValue(response); vi.stubGlobal("fetch", request);
    await expect(selectScenario("routine-charging", generation)).rejects.toThrow(); expect(request).toHaveBeenCalledOnce();
  }
});

it("rejects unknown scenario status instead of presenting a guessed preset", async () => {
  vi.useFakeTimers();
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ generation, ready: true, scenarioId: "unknown" }))));
  const emit = vi.fn(); const stop = pollDemoStatus(emit, vi.fn());
  await vi.advanceTimersByTimeAsync(0); stop();
  expect(emit.mock.calls[0]).toEqual([null, "Invalid scenario identity."]);
});

it("describes real charging or observation instead of promising a transfer in every scenario", () => {
  for (const id of ["routine-charging", "healthy-operations", "blocked-no-candidate"] as const) {
    const html = renderToString(<SimulationControls status={{ generation, ready: true, state: "paused", speed: "1", scenarioId: id }} busy={false} unavailable={false} error={null} onControl={() => {}} />);
    expect(html).not.toContain("Approval transfers ownership"); expect(html).not.toContain("disabled");
  }
});
