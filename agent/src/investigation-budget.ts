import type { LanguageModel } from "@mastra/core/llm";
import type { GroundingFailure } from "./recovery-grounding.js";

export const investigationLimits = Object.freeze({ requests: 6, tools: 8, milliseconds: 30_000,
  outputPerRequest: 6000, inputTokens: 32_000, outputTokens: 12_000, totalTokens: 44_000 });
type BudgetGuard = "model_busy" | "request_limit" | "prompt_size" | "output_allowance" | "reported_usage";
export type InvestigationTrace = { requests: number; toolCalls: number; inputTokens: number; outputTokens: number; totalTokens: number;
  usageReports?: number; reportedActualModel?: string; failureCategory?: string; groundingFailure?: GroundingFailure;
  budgetFailure?: { guard: BudgetGuard; promptBytes?: number };
  events: { kind: "request" | "tool"; name: string; input?: unknown; output?: unknown }[] };
type Model = Extract<LanguageModel, { specificationVersion: "v2" }>;

type UsageCounts = { inputTokens: number; outputTokens: number; totalTokens: number };

const count = (value: unknown): value is number => typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
const record = (value: unknown): Record<string, unknown> | undefined =>
  typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined;

function within(total: number, ...parts: unknown[]): boolean {
  let remaining = total;
  for (const part of parts) {
    if (part === undefined) continue;
    if (!count(part) || part > remaining) return false;
    remaining -= part;
  }
  return true;
}

function matchesDetails(value: unknown, total: number, known: Record<string, unknown>): boolean {
  if (value === undefined || value === null) return true;
  const details = record(value);
  return !!details && within(total, ...Object.keys(known).map(key => details[key] ?? undefined)) &&
    Object.entries(known).every(([key, expected]) => details[key] == null || expected === undefined || details[key] === expected);
}

export function normalizeInvestigationUsage(value: unknown): UsageCounts | undefined {
  const usage = record(value);
  if (!usage) return undefined;
  const input = record(usage.inputTokens), output = record(usage.outputTokens);
  const inputTokens = input ? input.total : usage.inputTokens;
  const outputTokens = output ? output.total : usage.outputTokens;
  if (!!input !== !!output || !count(inputTokens) || !count(outputTokens)) return undefined;
  const combined = inputTokens + outputTokens;
  const raw = record(usage.raw);
  const rawTotal = raw?.total_tokens;
  if (usage.raw !== undefined && !raw ||
      raw?.input_tokens !== undefined && raw.input_tokens !== inputTokens ||
      raw?.output_tokens !== undefined && raw.output_tokens !== outputTokens ||
      rawTotal !== undefined && usage.totalTokens !== undefined && rawTotal !== usage.totalTokens) return undefined;

  // The v3 SDK can omit a combined total. Flat v2 usage must report it.
  const totalTokens = input && usage.totalTokens === undefined
    ? rawTotal === undefined ? combined : rawTotal
    : usage.totalTokens;
  if (!count(totalTokens) || totalTokens < combined) return undefined;
  if (!within(inputTokens, usage.cachedInputTokens) || !within(totalTokens - inputTokens, usage.reasoningTokens)) return undefined;
  if (input && output && (!within(inputTokens, input.noCache, input.cacheRead, input.cacheWrite) ||
      !within(outputTokens, output.text, output.reasoning) ||
      usage.cachedInputTokens !== undefined && input.cacheRead !== undefined && usage.cachedInputTokens !== input.cacheRead ||
      usage.reasoningTokens !== undefined && output.reasoning !== undefined && usage.reasoningTokens !== output.reasoning)) return undefined;
  if (!matchesDetails(raw?.input_tokens_details, inputTokens, {
    cached_tokens: input?.cacheRead ?? usage.cachedInputTokens, cache_write_tokens: input?.cacheWrite,
  }) || !matchesDetails(raw?.output_tokens_details, input ? outputTokens : totalTokens - inputTokens, {
    reasoning_tokens: output?.reasoning ?? usage.reasoningTokens,
  })) return undefined;
  return { inputTokens, outputTokens, totalTokens };
}

export class InvestigationBudget {
  readonly controller = new AbortController();
  readonly trace: InvestigationTrace = { requests: 0, toolCalls: 0, inputTokens: 0, outputTokens: 0, totalTokens: 0, events: [], usageReports: 0 };
  private failed = false;
  private busy = false;
  private modelBusy = false;
  private readonly timer: ReturnType<typeof setTimeout>;
  private readonly expires = performance.now() + investigationLimits.milliseconds;
  constructor(signal?: AbortSignal) {
    this.timer = setTimeout(() => this.fail("timeout"), investigationLimits.milliseconds);
    if (signal?.aborted) this.fail("cancelled");
    signal?.addEventListener("abort", this.cancel, { once: true });
    this.externalSignal = signal;
  }
  private externalSignal?: AbortSignal;
  private cancel = () => { this.fail("cancelled"); };
  fail(category = "investigation_failed"): Error {
    this.trace.failureCategory ??= category;
    this.failed = true; this.controller.abort(); return new Error("Investigation stopped safely.");
  }
  private exhaust(guard: BudgetGuard, promptBytes?: number): Error {
    this.trace.budgetFailure ??= { guard, ...(promptBytes === undefined ? {} : { promptBytes }) };
    return this.fail("budget_exhausted");
  }
  check(): void {
    if (performance.now() >= this.expires) throw this.fail("timeout");
    if (this.failed || this.controller.signal.aborted) throw this.fail();
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
    if (this.busy || this.trace.toolCalls >= investigationLimits.tools) throw this.fail("budget_exhausted");
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
      if (this.modelBusy) throw this.exhaust("model_busy");
      if (this.trace.requests >= investigationLimits.requests) throw this.exhaust("request_limit");
      // UTF-8 bytes conservatively bound text token input, including tool and response schemas.
      const bytes = Buffer.byteLength(JSON.stringify({ prompt: options.prompt, tools: options.tools, responseFormat: options.responseFormat }));
      const outputRemaining = investigationLimits.outputTokens - this.trace.outputTokens;
      const totalRemaining = investigationLimits.totalTokens - this.trace.totalTokens - bytes;
      if (outputRemaining <= 0) throw this.exhaust("output_allowance");
      if (totalRemaining <= 0 || bytes > investigationLimits.inputTokens - this.trace.inputTokens) throw this.exhaust("prompt_size", bytes);
      const remaining = Math.min(outputRemaining, totalRemaining);
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
            if (part.type === "tool-call") { try { inspectTool(part.toolName, part.input); } catch { this.trace.failureCategory = "invalid_tool"; throw this.fail("invalid_tool"); } }
            if (part.type === "response-metadata" && typeof part.modelId === "string") {
              const identity = `${model.provider}/${part.modelId}`;
              if (/^[A-Za-z0-9._-]{1,32}\/[A-Za-z0-9._:-]{1,96}$/.test(identity) && !/sk-|secret|bearer|token/i.test(identity)) this.trace.reportedActualModel = identity;
            }
            if (part.type === "finish") {
              if (finished) throw this.fail();
              finished = true;
              const usage = normalizeInvestigationUsage(part.usage);
              if (!usage || usage.outputTokens > maxOutputTokens) throw this.fail("invalid_usage");
              this.trace.inputTokens += usage.inputTokens; this.trace.outputTokens += usage.outputTokens;
              this.trace.totalTokens += usage.totalTokens; this.trace.usageReports = (this.trace.usageReports ?? 0) + 1;
              if (this.trace.inputTokens > investigationLimits.inputTokens || this.trace.outputTokens > investigationLimits.outputTokens ||
                  this.trace.totalTokens > investigationLimits.totalTokens) throw this.exhaust("reported_usage");
            }
            controller.enqueue(part);
          },
          flush: () => { if (!finished) throw this.fail("invalid_usage"); this.modelBusy = false; },
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
