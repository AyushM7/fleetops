import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { chargingReasoningFixture } from "../../agent/src/fixtures.js";
import { foundationCases } from "./benchmark.js";
import { runRecovery, type RecoveryTransport } from "./recovery.js";

// Only test orchestration here; behavioral performance is measured in Go, never these doubles.
function inputs() {
  return Array.from({ length: 5 }, (_, i) => { const test = foundationCases()[0]; return {
    id: `unit-fixture-${i}`, source: "synthetic orchestration fixture; not simulator measurements", input: test.input,
    expectedAction: test.expected.action, initialHash: `unit-hash-${i}`, seed: 42, horizonTicks: 200,
  }; });
}
beforeEach(() => vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network authorized"); })));
afterEach(() => { expect(fetch).not.toHaveBeenCalled(); vi.unstubAllGlobals(); });

it("passes only detached fleet facts to the adapter and only validated action identities to the Go harness", async () => {
  const observations = inputs(); const before = structuredClone(observations);
  const adapter = vi.fn(async input => ({ reasoning: chargingReasoningFixture(input) }));
  let requests: unknown;
  const transport: RecoveryTransport = (mode, payload) => {
    if (mode === "observe") return observations;
    requests = payload; throw new Error("stop before behavioral measurement");
  };
  await expect(runRecovery(transport, adapter)).rejects.toThrow("stop before behavioral measurement");
  expect(observations).toEqual(before); expect(adapter.mock.calls[0]).toEqual([observations[0].input, observations[0].id]);
  expect(requests).toEqual(observations.map(o => ({ id:o.id, initialHash:o.initialHash, action:o.expectedAction })));
  expect(JSON.stringify(requests)).not.toMatch(/approved|operator|executeImmediately|rationale|evidence/);
});
it("rejects unsafe output before any treatment or operator workflow is invoked", async () => {
  const observations = inputs(); const transport = vi.fn<RecoveryTransport>().mockReturnValue(observations);
  await expect(runRecovery(transport,async input => ({ reasoning: { ...chargingReasoningFixture(input), proposedAction: { action: "RepairRobot" } } }))).rejects.toThrow("Recommendation rejected");
  expect(transport).toHaveBeenCalledTimes(1);
});
it("rejects duplicate/missing inputs without giving them to the adapter", async () => {
  const observations = inputs(); observations[1].id = observations[0].id;
  const adapter = vi.fn();
  await expect(runRecovery(() => observations,adapter)).rejects.toThrow("Missing or ambiguous");
  expect(adapter).not.toHaveBeenCalled();
});
it("does not treat missing behavioral results as completed experiments", async () => {
  const transport: RecoveryTransport = mode => mode === "observe" ? inputs() : [];
  await expect(runRecovery(transport)).rejects.toThrow("Incomplete Go experiment results");
});
it("rejects an adapter's snapshot mutation instead of applying it to the observed pair", async () => {
  const observations = inputs(); const before = structuredClone(observations);
  const transport = vi.fn<RecoveryTransport>().mockReturnValue(observations);
  await expect(runRecovery(transport,async input => { input.tick++; return { reasoning:chargingReasoningFixture(input) }; })).rejects.toThrow("Recommendation rejected");
  expect(observations).toEqual(before); expect(transport).toHaveBeenCalledTimes(1);
});
