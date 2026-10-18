import type { LanguageModel } from "@mastra/core/llm";

export const investigationLimits = Object.freeze({ requests: 6, tools: 8, milliseconds: 30_000,
  outputPerRequest: 6000, inputTokens: 32_000, outputTokens: 12_000, totalTokens: 44_000 });
export type InvestigationTrace = { requests: number; toolCalls: number; inputTokens: number; outputTokens: number; totalTokens: number;
  events: { kind: "request" | "tool"; name: string; input?: unknown; output?: unknown }[] };
type Model = Extract<LanguageModel, { specificationVersion: "v2" }>;

export class InvestigationBudget {
  readonly controller = new AbortController();
  readonly trace: InvestigationTrace = { requests: 0, toolCalls: 0, inputTokens: 0, outputTokens: 0, totalTokens: 0, events: [] };
  private failed = false;
  private busy = false;
  private modelBusy = false;
  private readonly timer: ReturnType<typeof setTimeout>;
  private readonly expires = performance.now() + investigationLimits.milliseconds;
  constructor(signal?: AbortSignal) {
    this.timer = setTimeout(() => this.fail(), investigationLimits.milliseconds);
    if (signal?.aborted) this.fail();
    signal?.addEventListener("abort", this.cancel, { once: true });
    this.externalSignal = signal;
  }
  private externalSignal?: AbortSignal;
  private cancel = () => { this.fail(); };
  fail(): Error {
    this.failed = true; this.controller.abort(); return new Error("Investigation stopped safely.");
  }
  check(): void {
    if (this.failed || this.controller.signal.aborted || performance.now() >= this.expires) throw this.fail();
  }
  close(): void {
    clearTimeout(this.timer); this.externalSignal?.removeEventListener("abort", this.cancel);
    this.controller.abort();
  }
  async wait<T>(operation: PromiseLike<T>): Promise<T> {
    this.check();
    const signal = this.controller.signal;
    let abort: () => void = () => {};
    try {
      return await Promise.race([Promise.resolve(operation), new Promise<never>((_, reject) => {
        abort = () => reject(this.fail()); signal.addEventListener("abort", abort, { once: true });
      })]);
    } finally { signal.removeEventListener("abort", abort); }
  }
  async tool<T>(name: string, input: unknown, read: () => T): Promise<T> {
    this.check();
    // Reject overlap even if a framework upgrade stops honoring toolCallConcurrency.
    if (this.busy || this.trace.toolCalls >= investigationLimits.tools) throw this.fail();
    this.busy = true; this.trace.toolCalls++;
    try {
      await Promise.resolve(); this.check();
      const output = structuredClone(read());
      this.trace.events.push({ kind: "tool", name, input: structuredClone(input), output: structuredClone(output) });
      return output;
    } catch { throw this.fail(); } finally { this.busy = false; }
  }
  model(model: Model, inspectTool: (name: string, input: string) => void): Model {
    const request = async (options: Parameters<Model["doGenerate"]>[0]) => {
      this.check();
      if (this.modelBusy || this.trace.requests >= investigationLimits.requests) throw this.fail();
      // UTF-8 bytes conservatively bound text token input, including tool and response schemas.
      const bytes = Buffer.byteLength(JSON.stringify({ prompt: options.prompt, tools: options.tools, responseFormat: options.responseFormat }));
      const remaining = Math.min(investigationLimits.outputTokens - this.trace.outputTokens, investigationLimits.totalTokens - this.trace.totalTokens - bytes);
      if (remaining <= 0 || bytes > investigationLimits.inputTokens - this.trace.inputTokens) throw this.fail();
      const maxOutputTokens = Math.min(options.maxOutputTokens ?? investigationLimits.outputPerRequest, investigationLimits.outputPerRequest, remaining);
      this.trace.requests++;
      this.modelBusy = true;
      this.trace.events.push({ kind: "request", name: model.modelId });
      try {
        const result = await this.wait(model.doGenerate({ ...options, abortSignal: this.controller.signal,
          maxOutputTokens }));
        let finished = false;
        return { ...result, stream: result.stream.pipeThrough(new TransformStream({
          transform: (part, controller) => {
            this.check();
            if (part.type === "error") throw this.fail();
            if (part.type === "tool-call") inspectTool(part.toolName, part.input);
            if (part.type === "finish") {
              if (finished) throw this.fail();
              finished = true;
              const { inputTokens, outputTokens, totalTokens } = part.usage;
              if (![inputTokens, outputTokens, totalTokens].every(n => Number.isSafeInteger(n) && n! >= 0) ||
                  totalTokens! < inputTokens! + outputTokens! || outputTokens! > maxOutputTokens) throw this.fail();
              this.trace.inputTokens += inputTokens!; this.trace.outputTokens += outputTokens!;
              this.trace.totalTokens += totalTokens!;
              if (this.trace.inputTokens > investigationLimits.inputTokens || this.trace.outputTokens > investigationLimits.outputTokens ||
                  this.trace.totalTokens > investigationLimits.totalTokens) throw this.fail();
            }
            controller.enqueue(part);
          },
          flush: () => { if (!finished) throw this.fail(); this.modelBusy = false; },
        })) };
      } catch { throw this.fail(); }
    };
    // Keep router getters/methods bound to their original instance (private fields).
    return new Proxy(model, { get: (target, key) => {
      if (key === "doGenerate" || key === "doStream") return request;
      const value = Reflect.get(target, key, target);
      return typeof value === "function" ? value.bind(target) : value;
    } });
  }
}
