import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToString } from "react-dom/server";
import { authentication } from "./demo";
import { pollDemoStatus, restartScenario, RestartControl } from "./restart";

const oldGeneration = "a".repeat(64);
const freshGeneration = "b".repeat(64);
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("scenario restart", () => {
  it("explains how to renew an expired or revoked visitor without relying on a local runner", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("expired", { status: 401 })));
    await expect(restartScenario(oldGeneration)).rejects.toThrow("Clear this site's cookies to begin a new visit.");
  });
  it("shows accessible progress, disables repeated clicks, and displays failures without claiming success", () => {
    const busy = renderToString(<RestartControl busy disabled={false} error={null} onRestart={() => {}} />);
    expect(busy).toContain("disabled"); expect(busy).toContain("Restarting scenario");
    expect(busy).toContain('aria-live="polite"'); expect(busy).toContain("capturing evidence");
    const failed = renderToString(<RestartControl busy={false} disabled={false} error="Restart was not confirmed" onRestart={() => {}} />);
    expect(failed).toContain('role="alert"'); expect(failed).toContain("Restart was not confirmed");
    expect(failed).not.toContain("disabled"); expect(failed).not.toContain("success");
  });
  it("sends only the fixed operation and expected generation, then validates fresh readiness", async () => {
    const request = vi.fn().mockResolvedValue(new Response(JSON.stringify({ generation: freshGeneration, ready: true })));
    vi.stubGlobal("fetch", request);
    expect(await restartScenario(oldGeneration)).toEqual({ generation: freshGeneration, ready: true });
    const [url, options] = request.mock.calls[0];
    expect(url).toBe("/demo/restart"); expect(options.body).toBe("{}");
    expect(options.credentials).toBe("same-origin"); expect(options.redirect).toBe("error");
    expect(options.headers["X-FleetOps-Generation"]).toBe(oldGeneration);
    expect(options.headers["X-FleetOps-Demo"]).toBe("1"); expect(options.headers.Authorization).toBeUndefined();
    vi.stubGlobal("document", { querySelector: () => ({ getAttribute: () => "enabled" }) });
    expect(authentication(oldGeneration).headers).toHaveProperty("X-FleetOps-Generation", oldGeneration);
  });
  it("rejects stale, unavailable, malformed, and unauthenticated restart acknowledgements", async () => {
    for (const response of [new Response("denied", { status: 409 }), new Response("failed", { status: 503 }), new Response("expired", { status: 401 }), new Response(JSON.stringify({ generation: freshGeneration, ready: false })), new Response(JSON.stringify({ generation: oldGeneration, ready: true })), new Response(JSON.stringify({ generation: "invalid", ready: true }))]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
      await expect(restartScenario(oldGeneration)).rejects.toThrow();
    }
  });
  it("refreshes generations across tabs and stops polling and late responses on cleanup", async () => {
    vi.useFakeTimers();
    let late: ((value: Response) => void) | undefined;
    const request = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ generation: oldGeneration, ready: true })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ generation: freshGeneration, ready: true })))
      .mockImplementationOnce(() => new Promise<Response>(resolve => { late = resolve; }));
    vi.stubGlobal("fetch", request);
    const emit = vi.fn(); const stop = pollDemoStatus(emit, vi.fn());
    await vi.advanceTimersByTimeAsync(0); expect(emit.mock.calls[0][0].generation).toBe(oldGeneration);
    await vi.advanceTimersByTimeAsync(1000); expect(emit.mock.calls[1][0].generation).toBe(freshGeneration);
    await vi.advanceTimersByTimeAsync(1000); stop();
    expect(request.mock.calls[2][1].signal.aborted).toBe(true);
    late?.(new Response(JSON.stringify({ generation: oldGeneration, ready: true })));
    await vi.advanceTimersByTimeAsync(10000); expect(emit).toHaveBeenCalledTimes(2); expect(request).toHaveBeenCalledTimes(3);
  });
  it("reports status outages, stopped runtimes and session expiry", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response("outage", { status: 503 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ generation: oldGeneration, ready: false })))
      .mockResolvedValueOnce(new Response("expired", { status: 401 })));
    const emit = vi.fn(); const unauthorized = vi.fn(); const stop = pollDemoStatus(emit, unauthorized);
    await vi.advanceTimersByTimeAsync(0); expect(emit.mock.calls[0][0]).toBeNull();
    await vi.advanceTimersByTimeAsync(1000); expect(emit.mock.calls[1][0].ready).toBe(false);
    expect(emit.mock.calls[1][1]).toContain("reconcile");
    await vi.advanceTimersByTimeAsync(1000); expect(unauthorized).toHaveBeenCalledOnce(); stop();
  });
});
