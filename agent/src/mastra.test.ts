import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import { afterEach, expect, it, vi } from "vitest";
import { instructions } from "./agent.js";
import { reasoningSchema } from "./contracts.js";
import { observedFacts, validateReasoning } from "./reasoning.js";
import { fleetFixture, offlineModelFixture, reasoningFixture } from "./fixtures.js";

afterEach(() => { vi.unstubAllGlobals(); });
it("runs the real Mastra generation and structured-output parser with a local provider adapter", async () => {
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); }));
  const state = fleetFixture(14); state.robots[0].jobId = "J1";
  state.activeIncidents = [{ robotId: "R1", type: "low_battery", severity: "high", telemetry: { ...state.robots[0] } }];
  const called = vi.fn();
  const agent = new Agent({ id: "offline-reasoning-test", name: "Offline reasoning", instructions,
    model: offlineModelFixture(reasoningFixture(state), called), maxRetries: 0 });
  agent.__setLogger(noopLogger);
  const output = await agent.generate(JSON.stringify({ observedFacts: observedFacts(state) }), {
    maxSteps: 1, toolChoice: "none", structuredOutput: { schema: reasoningSchema },
  });
  expect(validateReasoning(output.object, state)).toEqual(reasoningFixture(state));
  expect(called).toHaveBeenCalledTimes(1);
  expect(fetch).not.toHaveBeenCalled();
});
