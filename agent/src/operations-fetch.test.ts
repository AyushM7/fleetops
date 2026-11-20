import { afterEach, expect, it, vi } from "vitest";
import { operationsModelId, operationsOutputLimit, singleOperationsFetch } from "./operations-fetch.js";
import { makeOpenAIOperationsDecision } from "./operations-manager.js";

const endpoint = "https://api.openai.com/v1/responses";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

it("sends one real Mastra Responses request through the guard with a mocked OpenAI transport", async () => {
  vi.stubEnv("OPENAI_API_KEY", "fixture-transport");
  const decision = { kind: "abstain", reason: "no_safe_option" };
  const text = JSON.stringify({ decision });
  const item = { type: "message", id: "msg_fixture", role: "assistant", status: "completed",
    content: [{ type: "output_text", text, annotations: [] }] };
  const usage = { input_tokens: 41, output_tokens: 20, total_tokens: 61 };
  const response = { id: "resp_fixture", object: "response", created_at: 1, model: operationsModelId,
    status: "completed", service_tier: "default", output: [item], usage };
  const transport = vi.fn(async (request: Request) => {
    const body = await request.json();
    expect(request.url).toBe(endpoint);
    expect(body).toMatchObject({ model: operationsModelId, store: false, service_tier: "default",
      max_output_tokens: operationsOutputLimit });
    expect(body.tools ?? []).toEqual([]);
    expect(body.text.format).toMatchObject({ strict: true, type: "json_schema" });
    if (!body.stream) return Response.json(response);
    const chunks = [
      { type: "response.created", response },
      { type: "response.output_item.added", output_index: 0, item },
      { type: "response.output_text.delta", item_id: item.id, delta: text },
      { type: "response.output_item.done", output_index: 0, item },
      { type: "response.completed", response },
    ];
    return new Response(chunks.map(chunk => "data: " + JSON.stringify(chunk) + "\n\n").join(""),
      { headers: { "Content-Type": "text/event-stream" } });
  });
  vi.stubGlobal("fetch", singleOperationsFetch(transport as typeof fetch));
  const permit = vi.fn();
  const result = await makeOpenAIOperationsDecision('{"tick":1}', permit, AbortSignal.timeout(2_000));
  expect(result).toEqual({ decision, usage: { model: operationsModelId, inputTokens: 41, outputTokens: 20 } });
  expect(permit).toHaveBeenCalledTimes(1);
  expect(transport).toHaveBeenCalledTimes(1);
});

it("denies retries, paid tools and a different host without forwarding them", async () => {
  const transport = vi.fn(async () => new Response("{}"));
  const guarded = singleOperationsFetch(transport);
  const request = { method: "POST", body: JSON.stringify({ model: operationsModelId, store: false,
    service_tier: "default", max_output_tokens: operationsOutputLimit,
    input: [{ role: "user", content: [{ type: "input_text", text: "fixture" }] }],
    text: { format: { type: "json_schema", strict: true } } }) };
  await expect(guarded("https://example.invalid/v1/responses", request)).rejects.toThrow();
  await expect(guarded(endpoint, request)).resolves.toBeDefined();
  await expect(guarded(endpoint, request)).rejects.toThrow();
  await expect(singleOperationsFetch(transport)(endpoint, { ...request, body: JSON.stringify({
    ...JSON.parse(request.body), tools: [{ type: "web_search" }],
  }) })).rejects.toThrow();
  expect(transport).toHaveBeenCalledTimes(1);
});
