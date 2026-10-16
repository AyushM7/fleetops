import { renderToString } from "react-dom/server";
import { describe, it, expect } from "vitest";
import App, { Dashboard } from "./App";
import type { Feed, FleetSnapshot } from "./fleet";

const snapshot: FleetSnapshot = {
  tick: 43, poisoned: false,
  layout: { width: 4, height: 4, cells: Array.from({ length: 16 }, (_, i) => ({ x: i % 4, y: Math.floor(i / 4), kind: i === 0 ? "station" : i === 4 ? "wall" : i % 4 === 1 ? "aisle" : "free" })) },
  stations: [{ id: "S1", x: 0, y: 0, occupiedBy: "", reservedBy: "" }],
  robots: [{ id: "R1", x: 2, y: 2, status: "moving", battery: 28, jobId: "J8", blocked: true, waitTicks: 41 },
    { id: "R2", x: 3, y: 3, status: "offline", battery: 74, jobId: "", blocked: false, waitTicks: 0 },
    { id: "R3", x: 3, y: 0, status: "faulted", battery: 60, jobId: "", blocked: false, waitTicks: 0 }],
};
const live: Feed = { phase: "live", snapshot, updatedAt: new Date("2026-10-16T12:00:00Z") };
describe("dashboard", () => {
  it("starts without robot data and with a masked memory-only sign-in", () => {
    const html = renderToString(<App />);
    expect(html).toContain("FleetOps"); expect(html).toContain('type="password"'); expect(html).toContain('autoComplete="off"');
    expect(html).toContain("stays in memory"); expect(html).not.toContain("R1");
  });
  it("renders actual coordinates, layout, tick, assignments, battery and explicit fault states", () => {
    const html = renderToString(<Dashboard feed={live} />).replace(/<!--.*?-->/g, "");
    for (const value of ["43", "R1", "J8", "28%", "Moving", "Traffic blocked", "41 ticks", "Offline", "Robot unresponsive", "Faulted", "Robot fault reported", "cell-wall", "cell-station", "cell-aisle", "translate(100,98)"]) expect(html).toContain(value);
    expect(html).not.toContain("SetCharging"); expect(html).not.toContain("approveProposal");
  });
  it("explains loading, unavailable snapshots and poisoned runtime", () => {
    expect(renderToString(<Dashboard feed={{ phase: "loading", snapshot: null, updatedAt: null }} />)).toContain("Loading the warehouse");
    const stale = renderToString(<Dashboard feed={{ ...live, phase: "unavailable" }} />);
    expect(stale).toContain("Backend unavailable"); expect(stale).toContain("positions may be stale"); expect(stale).toContain("43");
    expect(renderToString(<Dashboard feed={{ ...live, snapshot: { ...snapshot, poisoned: true } }} />)).toContain("Runtime requires reconciliation");
  });
});
