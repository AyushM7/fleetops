import { afterEach, expect, it, vi } from "vitest";
import { evaluationPrompt, generateEvaluationResponse } from "./evaluation-model.js";
import { reasoningPrompt } from "./agent.js";
import { fleetFixture, offlineModelFixture, reasoningFixture } from "./fixtures.js";
import { validateReasoning } from "./reasoning.js";

afterEach(() => vi.unstubAllGlobals());
it("removes only the supported action answer in evaluation; production prompt is unchanged", () => {
  const state = fleetFixture(50);
  const assisted = JSON.parse(reasoningPrompt(state));
  const reduced = JSON.parse(evaluationPrompt(state, "reduced-hint"));
  expect(evaluationPrompt(state)).toBe(reasoningPrompt(state));
  expect(assisted.supportedAction.action).toBe("SetCharging");
  delete assisted.supportedAction;
  expect(reduced).toEqual(assisted);
  expect(JSON.parse(reasoningPrompt(state)).supportedAction).toBeTruthy();
});
it("keeps generation tool-free and host grounding independent of prompt condition", async () => {
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted"); }));
  const state = fleetFixture(50);
  const invalid = { ...reasoningFixture(state), tick: state.tick - 1 };
  const call = vi.fn();
  const result = await generateEvaluationResponse(state, offlineModelFixture(invalid, call), { maxOutputTokens: 2400, promptVariant: "reduced-hint" });
  expect(call).toHaveBeenCalledTimes(1);
  expect(result.callFailed).toBe(false);
  expect(() => validateReasoning(JSON.parse(result.text!), state)).toThrow();
  expect(result.latencyMs).toBeGreaterThanOrEqual(0);
  expect(fetch).not.toHaveBeenCalled();
});
it("uses Responses output ceiling and suppresses provider errors and credentials", async () => {
  const fetcher = vi.fn(async (_url: unknown, options?: RequestInit) => {
    const body = JSON.parse(String(options?.body));
    expect(body.model).toBe("gpt-6-luna");
    expect(body.max_output_tokens).toBe(2400);
    expect(body.store).toBe(false);
    expect(body.service_tier).toBe("default");
    expect(body.tools ?? []).toEqual([]);
    return new Response(JSON.stringify({ error: { message: "sensitive provider error" } }), { status: 503, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetcher);
  const response = await generateEvaluationResponse(fleetFixture(), { providerId: "openai", modelId: "gpt-6-luna", apiKey: "test-only", url: "https://api.openai.com/v1", api: "responses" }, { maxOutputTokens: 2400 });
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(response.callFailed).toBe(true);
  expect(JSON.stringify(response)).not.toMatch(/sensitive|test-only|authorization/i);
});
