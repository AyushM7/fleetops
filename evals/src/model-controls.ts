import { createHash } from "node:crypto";
import { hasProviderKey } from "../../agent/src/config.js";

export interface ModelConfig {
  mode: "offline-model-double" | "live-model";
  provider: string; model: string; trials: number; maxRequests: number;
  maxOutputTokens: number; temperature?: number;
  promptVariant?: "assisted" | "reduced-hint" | "both";
}
// An operator-reviewed upper bound, not a built-in or automatically refreshed price list.
export interface CostReview {
  provider: string; model: string; source: string; reviewedAt: string; expiresAt: string;
  maxBillableInputTokens: number; inputUSDPerMillion: number; outputUSDPerMillion: number;
  outputLimitIncludesAllBilledTokens: true;
}
export function checkConfig(config: ModelConfig, scenarios: number): number {
  for (const n of [config.trials, config.maxRequests, config.maxOutputTokens, scenarios]) {
    if (!Number.isSafeInteger(n) || n < 1) throw new Error("Positive integer limits and nonempty scenarios required.");
  }
  if (!["offline-model-double", "live-model"].includes(config.mode)) throw new Error("Unknown model mode.");
  if (config.temperature !== undefined && (!Number.isFinite(config.temperature) || config.temperature < 0 || config.temperature > 2)) throw new Error("Invalid temperature.");
  if (config.promptVariant !== undefined && !["assisted", "reduced-hint", "both"].includes(config.promptVariant)) throw new Error("Unknown prompt variant.");
  const requests = scenarios * config.trials * (config.promptVariant === "both" ? 2 : 1);
  if (!Number.isSafeInteger(requests) || requests > config.maxRequests) throw new Error("Request ceiling exceeded before generation.");
  if (config.mode === "offline-model-double" && (config.provider !== "offline-fixture" || config.model !== "deterministic")) throw new Error("Offline mode permits only the local double.");
  return requests;
}
export function verifyLiveCost(config: ModelConfig, requests: number, rawReview: string | undefined,
  approvedHash: string | undefined, budgetUSD: number, env: NodeJS.ProcessEnv, now = Date.now()) {
  checkConfig(config, 1);
  if (env.CI && env.CI !== "false" && env.CI !== "0") throw new Error("Live evaluation is disabled in CI.");
  if (!Number.isSafeInteger(requests) || requests < 1 || requests > config.maxRequests) throw new Error("Request ceiling invalid.");
  if (config.mode !== "live-model" || !["openai", "anthropic", "google"].includes(config.provider) ||
    !/^[a-zA-Z0-9._:/-]+$/.test(config.model)) throw new Error("Explicit supported provider/model required.");
  if (!rawReview || !approvedHash || createHash("sha256").update(rawReview).digest("hex") !== approvedHash) {
    throw new Error("Pricing unavailable or unreviewed: live execution blocked. Supply and approve a cost-review file hash.");
  }
  const r = JSON.parse(rawReview) as CostReview;
  if (r.provider !== config.provider || r.model !== config.model || typeof r.source !== "string" || !r.source.trim() ||
    !Number.isFinite(Date.parse(r.reviewedAt)) || Date.parse(r.reviewedAt) > now ||
    !Number.isFinite(Date.parse(r.expiresAt)) || Date.parse(r.expiresAt) <= now ||
    Date.parse(r.expiresAt) <= Date.parse(r.reviewedAt) ||
    !Number.isSafeInteger(r.maxBillableInputTokens) || r.maxBillableInputTokens < 1 ||
    ![r.inputUSDPerMillion, r.outputUSDPerMillion].every(n => typeof n === "number" && Number.isFinite(n) && n > 0) ||
    r.outputLimitIncludesAllBilledTokens !== true) throw new Error("Cost bounds cannot be verified against reviewed configuration.");
  const estimatedMaximumUSD = requests * (r.maxBillableInputTokens * r.inputUSDPerMillion + config.maxOutputTokens * r.outputUSDPerMillion) / 1_000_000;
  if (!Number.isFinite(budgetUSD) || budgetUSD <= 0 || !Number.isFinite(estimatedMaximumUSD) || estimatedMaximumUSD > budgetUSD) throw new Error("Estimated maximum exceeds spending ceiling or budget missing.");
  if (!hasProviderKey(`${config.provider}/${config.model}`, env)) throw new Error("Provider credential missing.");
  return { estimatedMaximumUSD, budgetUSD, pricingSource: r.source, reviewSHA256: approvedHash,
    verification: "operator-reviewed provider context/billing bound" as const };
}
