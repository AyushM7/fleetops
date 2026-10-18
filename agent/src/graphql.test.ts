import { randomBytes } from "node:crypto";
import { describe, expect, it, vi } from "vitest";
import { createFleetClient, fleetQuery, submitMutation } from "./graphql.js";
import { fleetFixture, pendingFixture } from "./fixtures.js";

const token = randomBytes(32).toString("hex");
const endpoint = "http://127.0.0.1:8080/graphql";
const input = { robotId: "R1", stationId: "S1", expiresAt: "2026-10-16T12:01:00Z", expectedObservationId: "a".repeat(64) };
describe("typed GraphQL transport", () => {
  it("uses only supported operations, authenticates privately and strips extra fields", async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json({ data: { fleetState: fleetFixture() } }))
      .mockResolvedValueOnce(Response.json({ data: { submitProposal: pendingFixture } }));
    const api = createFleetClient(endpoint, token, transport);
    expect(await api.readFleet()).toEqual(fleetFixture());
    expect(await api.submitCharging(input)).toEqual(pendingFixture);
    for (const [index, query] of [fleetQuery, submitMutation].entries()) {
      const init = transport.mock.calls[index][1]!;
      expect(init.headers).toEqual({ "Content-Type": "application/json", Authorization: `Bearer ${token}` });
      expect(init.redirect).toBe("error");
      expect(JSON.parse(init.body as string)).toEqual({ query, variables: index ? { input } : {} });
      expect(init.body).not.toContain(token);
      expect(init.signal).toBeInstanceOf(AbortSignal);
    }
  });
  it.each([
    [401, {}, "authentication"], [403, {}, "authentication"], [503, {}, "unavailable"],
    [200, { errors: [{ message: token }] }, "graphql"],
    [200, { data: { fleetState: { ...fleetFixture(), robots: [{}] } } }, "malformed"],
    [200, { data: null }, "malformed"], [200, {}, "malformed"],
  ])("sanitizes backend failure %s", async (status, body, code) => {
    const api = createFleetClient(endpoint, token, vi.fn<typeof fetch>().mockResolvedValue(Response.json(body, { status })));
    await expect(api.readFleet()).rejects.toThrow(`Fleet API: ${code}`);
    await expect(api.readFleet()).rejects.not.toThrow(token);
  });
  it("handles unavailable transport, invalid JSON and invalid mutation shapes", async () => {
    for (const response of [new Response("not json"), Response.json({ data: { submitProposal: { state: "pending" } } })]) {
      const api = createFleetClient(endpoint, token, vi.fn<typeof fetch>().mockResolvedValue(response));
      await expect(api.submitCharging(input)).rejects.toThrow("malformed");
    }
    const api = createFleetClient(endpoint, token, vi.fn<typeof fetch>().mockRejectedValue(new Error(token)));
    await expect(api.readFleet()).rejects.toThrow("unavailable");
  });
  it("rejects malformed input before transport", async () => {
    const transport = vi.fn<typeof fetch>();
    await expect(createFleetClient(endpoint, token, transport).submitCharging({ ...input, expiresAt: "bad" })).rejects.toThrow("configuration");
    expect(transport).not.toHaveBeenCalled();
  });
  it.each([undefined, "", "fabricated"])("rejects missing or malformed observation identity %s before transport", async expectedObservationId => {
    const transport = vi.fn<typeof fetch>();
    const api = createFleetClient(endpoint, token, transport);
    await expect(api.submitCharging({ ...input, expectedObservationId } as never)).rejects.toThrow("configuration");
    await expect(api.submitReassignment({ jobId: "J1", fromRobotId: "R1", toRobotId: "R3", expiresAt: input.expiresAt, expectedObservationId } as never)).rejects.toThrow("configuration");
    expect(transport).not.toHaveBeenCalled();
  });
  it("requires a live identity on production reads while archived snapshots remain parseable", async () => {
    const { observationId: _identity, ...archived } = fleetFixture();
    const transport = vi.fn<typeof fetch>().mockResolvedValue(Response.json({ data: { fleetState: archived } }));
    await expect(createFleetClient(endpoint, token, transport).readFleet()).rejects.toThrow("malformed");
  });
  it.each(["http://example.com/graphql", "https://user:pass@example.com/graphql", `${endpoint}?token=x`, "invalid"])("rejects unsafe endpoint %s", url => {
    expect(() => createFleetClient(url, token)).toThrow("configuration");
  });
});
