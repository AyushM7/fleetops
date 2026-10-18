import { createHash } from "node:crypto";

export interface Config {
  live: boolean; plan: boolean; go: boolean; scenarios: string[]; trials: number; model: string | null; provider: "openai" | null;
  maxRequests: number; maxOutputTokens: number; maxElapsedMs: number; budgetUSD: number | null; sourceRevision: string | null;
  costReview?: string; approveCostSHA256?: string; approvePlanSHA256?: string;
  testHarnessApprovals?: boolean;
}
export interface PriceReview {
  version: "agent07-price-v1"; provider: "openai"; model: string; source: string; modelSource: string;
  reviewedAt: string; expiresAt: string; acknowledged: true; immutableModelAcknowledged: true;
  contextWindowTokens: number; modelMaxOutputTokens: number; inputUSDPerMillion: number; outputUSDPerMillion: number;
  ancillaryUSDPerRequest: number; highestApplicableTierAcknowledged: true; outputCapIncludesReasoning: true;
  responsesOutputCapSupported: true; allFeesIncludedAcknowledged: true;
}
export const sha256 = (raw: string): string => createHash("sha256").update(raw).digest("hex");
const fail = (): never => { throw new Error("Live evaluation blocked: exact reviewed model, current pricing/context/all-fee bounds, approved hashes and finite budget required."); };
function official(value: unknown): boolean {
  if (typeof value !== "string") return false;
  try { const u = new URL(value); return u.protocol === "https:" && ["developers.openai.com", "platform.openai.com"].includes(u.hostname) && !u.username && !u.password; } catch { return false; }
}
export function bounds(c: Config, raw?: string, now = Date.now()) {
  const trials = c.scenarios.length * c.trials, requests = trials * 6;
  if (!c.scenarios.length || new Set(c.scenarios).size !== c.scenarios.length || !Number.isSafeInteger(c.trials) || c.trials < 1 || c.trials > 3 ||
    c.live && c.scenarios.length > 4 || !Number.isSafeInteger(c.maxRequests) || c.maxRequests < requests || c.maxRequests > 216 ||
    !Number.isSafeInteger(c.maxOutputTokens) || c.maxOutputTokens < 1 || c.maxOutputTokens > 6000 ||
    !Number.isSafeInteger(c.maxElapsedMs) || c.maxElapsedMs < 1000 || c.maxElapsedMs > 600_000) return fail();
  const base = { trials, maximumModelRequests: requests, maximumOutputTokens: requests * c.maxOutputTokens,
    maximumElapsedMs: c.maxElapsedMs, proposalSubmission: c.go && c.testHarnessApprovals ? "isolated-test-harness-only" : false, testHarnessApprovals: !!c.testHarnessApprovals, automaticRetries: 0, maximumToolsPerTrial: 8 };
  if(c.testHarnessApprovals && !c.go) return fail();
  if (!c.live) return { ...base, providerRequests: 0, estimatedMaximumUSD: null, maximumInputTokens: null, perRequestUSD: null, priceReviewSHA256: null };
  if (!c.go || c.provider !== "openai" || !c.model || !/^[A-Za-z0-9._:-]{1,96}$/.test(c.model) || /sk-|secret|bearer|token/i.test(c.model) ||
    !c.sourceRevision || !/^[a-f0-9]{40}$/.test(c.sourceRevision) || !raw || !c.approveCostSHA256 || sha256(raw) !== c.approveCostSHA256) return fail();
  let r: PriceReview; try { r = JSON.parse(raw) as PriceReview; } catch { return fail(); }
  if (r.version !== "agent07-price-v1" || r.provider !== c.provider || r.model !== c.model || !official(r.source) || !official(r.modelSource) ||
    !Number.isFinite(Date.parse(r.reviewedAt)) || Date.parse(r.reviewedAt) > now || now - Date.parse(r.reviewedAt) > 7 * 86400_000 ||
    !Number.isFinite(Date.parse(r.expiresAt)) || Date.parse(r.expiresAt) <= now || Date.parse(r.expiresAt) > Date.parse(r.reviewedAt) + 7 * 86400_000 ||
    [r.acknowledged, r.immutableModelAcknowledged, r.highestApplicableTierAcknowledged, r.outputCapIncludesReasoning, r.responsesOutputCapSupported, r.allFeesIncludedAcknowledged].some(v => v !== true) ||
    !Number.isSafeInteger(r.contextWindowTokens) || r.contextWindowTokens < 1 || r.contextWindowTokens > 2_000_000 ||
    !Number.isSafeInteger(r.modelMaxOutputTokens) || r.modelMaxOutputTokens < c.maxOutputTokens || r.modelMaxOutputTokens > r.contextWindowTokens ||
    ![r.inputUSDPerMillion, r.outputUSDPerMillion].every(n => Number.isFinite(n) && n > 0) || !Number.isFinite(r.ancillaryUSDPerRequest) || r.ancillaryUSDPerRequest < 0) return fail();
  // Reserve a full context at the highest acknowledged input tier on EVERY request,
  // including repeated tool/schema context; output includes billed reasoning tokens.
  const perRequestUSD = Math.ceil(((r.contextWindowTokens * r.inputUSDPerMillion + c.maxOutputTokens * r.outputUSDPerMillion) / 1_000_000 + r.ancillaryUSDPerRequest) * 1_000_000) / 1_000_000;
  const estimatedMaximumUSD = Math.ceil(perRequestUSD * requests * 1_000_000) / 1_000_000;
  if (!Number.isFinite(c.budgetUSD) || c.budgetUSD! <= 0 || !Number.isFinite(estimatedMaximumUSD) || !Number.isSafeInteger(Math.ceil(estimatedMaximumUSD*1_000_000)) || !Number.isSafeInteger(Math.floor(c.budgetUSD!*1_000_000)) || estimatedMaximumUSD > c.budgetUSD!) return fail();
  return { ...base, providerRequests: requests, estimatedMaximumUSD, perRequestUSD, maximumInputTokens: requests * r.contextWindowTokens, priceReviewSHA256: c.approveCostSHA256 };
}
export function planHash(config: Config, plan: ReturnType<typeof bounds>, datasetHash: string, sourceDigest: string): string {
  return sha256(JSON.stringify({ version: "agent07-v1", scenarios: config.scenarios, trials: config.trials, model: config.model, provider: config.provider,
    sourceRevision: config.sourceRevision, sourceDigest, datasetHash, plan, budgetUSD: config.budgetUSD, costReview: config.approveCostSHA256 ?? null }));
}
export function permitLive(c: Config, plan: ReturnType<typeof bounds>, hash: string, env: NodeJS.ProcessEnv): void {
  if (!c.live || c.plan || env.CI || env.VITEST || env.NODE_ENV === "test" || c.approvePlanSHA256 !== hash || !plan.priceReviewSHA256 || !env.OPENAI_API_KEY) return fail();
}
export class RequestReserve {
  calls = 0; private reservedMicros=0;
  private readonly perCallMicros: number; private readonly budgetMicros: number;
  get reservedUSD(): number { return this.reservedMicros/1_000_000; }
  constructor(private readonly maximum: number, perCallUSD: number, budgetUSD: number, private readonly expires: number) {
    this.perCallMicros=Math.ceil(perCallUSD*1_000_000); this.budgetMicros=Math.floor(budgetUSD*1_000_000);
    if (!Number.isSafeInteger(maximum) || maximum<1 || !Number.isSafeInteger(this.perCallMicros) || this.perCallMicros<1 || !Number.isSafeInteger(this.budgetMicros) || !Number.isFinite(expires)) throw new Error("Invalid request reservation.");
  }
  take(): void {
    if (Date.now() >= this.expires || this.calls >= this.maximum || this.reservedMicros + this.perCallMicros > this.budgetMicros) throw new Error("Evaluation request/cost/time reservation exhausted; no retry.");
    this.calls++; this.reservedMicros += this.perCallMicros;
  }
}
