import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fleetFixture } from "./fixtures.js";
import { observeFleet } from "./observe.js";
import { observedFacts } from "./reasoning.js";
import type { AgentResult } from "./contracts.js";

const runners = vi.hoisted(() => ({ runOffline: vi.fn(), runLive: vi.fn() }));
vi.mock("./agent.js", () => runners);
vi.mock("./graphql.js", () => ({ createFleetClient: vi.fn(() => ({})), FleetError: class extends Error {} }));
const originalArgs = process.argv;
const originalExitCode = process.exitCode;
function resultFixture(): AgentResult {
  const state = fleetFixture();
  return { ...observeFleet(state), observedFacts: observedFacts(state),
    aiAnalysis: { status: "disabled", interpretations: [], proposedAction: null },
    submissionCheck: "disabled", proposal: { status: "disabled" } };
}
beforeEach(() => {
  vi.resetModules(); vi.clearAllMocks();
  vi.stubEnv("FLEETOPS_PROPOSER_TOKEN", "test-only-private-token");
  process.argv = [originalArgs[0], "cli.js"];
  process.exitCode = undefined;
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  process.argv = originalArgs; process.exitCode = originalExitCode;
  vi.restoreAllMocks(); vi.unstubAllEnvs();
});
it.each(["rejected", "failed"] as const)("returns exit 1 for %s AI analysis", async status => {
  const result = resultFixture(); result.aiAnalysis.status = status;
  runners.runOffline.mockResolvedValue(result);
  await import("./cli.js");
  await vi.waitFor(() => expect(process.exitCode).toBe(1));
  expect(runners.runLive).not.toHaveBeenCalled();
});
it.each(["snapshot_changed", "unavailable"] as const)("returns exit 1 for %s before submission", async status => {
  const result = resultFixture(); result.submissionCheck = status;
  runners.runOffline.mockResolvedValue(result);
  await import("./cli.js");
  await vi.waitFor(() => expect(process.exitCode).toBe(1));
});
it("prints structured offline results without enabling a model", async () => {
  runners.runOffline.mockResolvedValue(resultFixture());
  await import("./cli.js");
  await vi.waitFor(() => expect(console.log).toHaveBeenCalled());
  expect(process.exitCode).toBeUndefined();
  expect(runners.runLive).not.toHaveBeenCalled();
  expect(JSON.stringify(vi.mocked(console.log).mock.calls)).not.toContain("test-only-private-token");
});
it("sanitizes provider or private configuration errors", async () => {
  runners.runOffline.mockRejectedValue(new Error("test-only-private-token"));
  await import("./cli.js");
  await vi.waitFor(() => expect(process.exitCode).toBe(1));
  expect(JSON.stringify(vi.mocked(console.error).mock.calls)).not.toContain("test-only-private-token");
});
