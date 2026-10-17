import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToString } from "react-dom/server";
import App from "./App";
import { authentication, isLocalDemo } from "./demo";
import { fetchFleet } from "./fleet";
import { decideProposal } from "./proposals";

const enableDemo = () => vi.stubGlobal("document", { querySelector: () => ({ getAttribute: () => "enabled" }) });
afterEach(() => vi.unstubAllGlobals());

describe("local demo", () => {
  it("preserves normal authenticated mode without the server marker", () => {
    expect(isLocalDemo()).toBe(false);
    expect(authentication("normal-token")).toEqual({ credentials: "omit", headers: { "Content-Type": "application/json", Authorization: "Bearer normal-token" } });
    expect(renderToString(<App />)).toContain('type="password"');
  });
  it("opens directly to the warehouse with an explicit simulation disclosure", () => {
    enableDemo();
    const html = renderToString(<App />);
    expect(html).toContain("Local demo");
    expect(html).toContain("No live LLM");
    expect(html).toContain("Loading the warehouse");
    expect(html).toContain("Approval inbox");
    expect(html).not.toContain('type="password"');
    expect(html).not.toContain("Bearer token");
    expect(html).not.toContain("local-demo-session");
  });
  it("uses same-origin cookies and CSRF header for reads and explicit decisions without a bearer", async () => {
    enableDemo();
    const request = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { fleetState: { tick: 42, poisoned: false, layout: { width: 1, height: 1, cells: [{ x: 0, y: 0, kind: "free" }] }, robots: [], stations: [], jobs: [], activeIncidents: [] } } })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { approveProposal: { proposalId: "p1", state: "approved", execution: "succeeded", auditCommitted: true, executionAttempted: true, error: null } } })));
    vi.stubGlobal("fetch", request);
    await fetchFleet("", new AbortController().signal);
    expect(request).toHaveBeenCalledTimes(1);
    expect(request.mock.calls[0][1]?.body).not.toContain("mutation");
    await decideProposal("", "p1", "approve", new AbortController().signal);
    for (const [, options] of request.mock.calls) {
      expect(options?.credentials).toBe("same-origin");
      expect(options?.headers).toEqual({ "Content-Type": "application/json", "X-FleetOps-Demo": "1" });
    }
  });
});
