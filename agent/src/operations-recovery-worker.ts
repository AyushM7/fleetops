import { createInterface } from "node:readline";
import { agentRunHandleSchema, type AgentRunCompletion } from "./agent-run.js";
import { validateSnapshot } from "./reasoning.js";
import { runRecoveryDecisionV3 } from "./decision-agent.js";
import { operationsModelId, writeOperationFrame } from "./operations-fetch.js";

export async function runOperationsRecoveryWorker() {
  const reader = createInterface({ input: process.stdin, crlfDelay: Infinity });
  const lines = reader[Symbol.asyncIterator]();
  const read = async (maximum: number): Promise<unknown> => {
    const line = await lines.next();
    if (line.done || Buffer.byteLength(line.value) > maximum) throw new Error("Recovery worker frame unavailable.");
    return JSON.parse(line.value);
  };
  try {
    const input = await read(64 * 1024) as Record<string, unknown>;
    const handle = agentRunHandleSchema.parse(input);
    const snapshot = validateSnapshot(input.snapshot);
    if (snapshot.observationId !== handle.observationId) throw new Error("Recovery observation does not match.");
    let pending = false;
    globalThis.fetch = async (resource, init) => {
      const request = new Request(resource, init);
      if (pending || request.url !== "https://api.openai.com/v1/responses" || request.method !== "POST") {
        throw new Error("Recovery transport denied.");
      }
      pending = true;
      try {
        const text = await request.text();
        if (Buffer.byteLength(text) > 256 * 1024) throw new Error("Recovery request too large.");
        request.signal.throwIfAborted();
        await writeOperationFrame({ request: JSON.parse(text) });
        return new Response(JSON.stringify(await read(1024 * 1024)), { headers: { "Content-Type": "application/json" } });
      } finally {
        pending = false;
      }
    };
    let completion: AgentRunCompletion | undefined;
    const denied = async () => { throw new Error("The investigation cannot issue commands."); };
    await runRecoveryDecisionV3({
      async readFleet() { return snapshot; },
      async startAgentRun(id) {
        if (id !== handle.observationId) throw new Error("Recovery observation does not match.");
        return handle;
      },
      async finishAgentRun(id, input) {
        if (id !== handle.id || completion || input.submit) throw new Error("Invalid recovery completion.");
        completion = input.completion;
        return { runId: id, recorded: true, denial: "", proposalResult: null };
      },
      submitCharging: denied,
      submitReassignment: denied,
    }, {
      enabled: true, model: { providerId: "openai", modelId: operationsModelId, url: "https://api.openai.com/v1", api: "responses", apiKey: "broker-only" },
      // The SDK must serialize continuation items for stateless requests.
      providerOptions: { openai: { store: false, serviceTier: "default", include: ["reasoning.encrypted_content"] } },
      objective: "compare_tradeoffs", source: "live-model", expectedObservationId: handle.observationId,
    });
    if (!completion) throw new Error("Recovery completion unavailable.");
    await writeOperationFrame({ completion });
  } finally {
    reader.close();
  }
}
