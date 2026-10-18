import { expect, it, vi } from "vitest";
import { capEvaluationModel, type EvaluationModel, type EvaluationRequest } from "./evaluation-investigation-model.js";

it("caps the actual v2 model boundary and uses reviewed non-stored standard-tier Responses settings", async () => {
  const generate = vi.fn(async (_request: EvaluationRequest) => ({ stream: new ReadableStream() }));
  const model: EvaluationModel = { specificationVersion: "v2", provider: "openai", modelId: "reviewed-test-snapshot", supportedUrls: {}, doGenerate: generate, doStream: generate };
  const permit = vi.fn(), guarded = capEvaluationModel(model, 123, permit);
  await guarded.doGenerate({ prompt: [], maxOutputTokens: 6000 });
  expect(permit).toHaveBeenCalledOnce(); expect(generate.mock.calls[0][0]).toMatchObject({ maxOutputTokens: 123, providerOptions: { openai: { store: false, serviceTier: "default" } } });
  expect(guarded.modelId).toBe("reviewed-test-snapshot");
});
it("rejects invalid caps and never reaches the model when the local permit is denied", () => {
  const generate = vi.fn(), model = { specificationVersion: "v2", doGenerate: generate } as unknown as EvaluationModel;
  expect(() => capEvaluationModel(model, 6001, vi.fn())).toThrow();
  const guarded = capEvaluationModel(model, 1, () => { throw new Error("budget stopped"); });
  expect(() => guarded.doGenerate({ prompt: [] })).toThrow("budget stopped"); expect(generate).not.toHaveBeenCalled();
});
