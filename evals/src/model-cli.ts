import { readFileSync } from "node:fs";
import { generateEvaluationResponse } from "../../agent/src/evaluation-model.js";
import { defaultModelConfig, decisionScenarios, runModelEvaluation, summarizeModel } from "./model-runner.js";
import { checkConfig, verifyLiveCost, type ModelConfig } from "./model-controls.js";

export function parseModelArgs(args: string[]): ModelConfig {
  const allowed = ["--model-eval", "--live", "--provider", "--model", "--trials", "--max-requests", "--max-output-tokens", "--temperature",
    "--cost-review", "--approve-cost-sha256", "--budget-usd", "--plan"];
  const seen = new Set<string>();
  for (let i = 0; i < args.length; i++) {
    const key = args[i];
    if (!allowed.includes(key) || seen.has(key)) throw new Error("Unknown or duplicate model evaluation option.");
    seen.add(key);
    if (!["--model-eval", "--live", "--plan"].includes(key) && (!args[++i] || args[i].startsWith("--"))) throw new Error("Option value missing.");
  }
  const value = (key: string) => args.includes(key) ? args[args.indexOf(key) + 1] : undefined;
  const number = (key: string, fallback: number) => value(key) === undefined ? fallback : Number(value(key));
  const live = args.includes("--live");
  if (live && (!value("--provider") || !value("--model"))) throw new Error("Live mode requires explicit provider and model.");
  return { ...defaultModelConfig, mode: live ? "live-model" : "offline-model-double",
    provider: value("--provider") ?? defaultModelConfig.provider, model: value("--model") ?? defaultModelConfig.model,
    trials: number("--trials", 1), maxRequests: number("--max-requests", 12), maxOutputTokens: number("--max-output-tokens", 2400),
    temperature: value("--temperature") === undefined ? undefined : number("--temperature", 0) };
}
export async function modelCLI(args: string[]): Promise<void> {
  const config = parseModelArgs(args);
  const requests = checkConfig(config, decisionScenarios().length);
  const value = (key: string) => args.includes(key) ? args[args.indexOf(key) + 1] : undefined;
  let cost: ReturnType<typeof verifyLiveCost> | undefined;
  if (config.mode === "live-model") {
    const file = value("--cost-review");
    cost = verifyLiveCost(config, requests, file ? readFileSync(file, "utf8") : undefined,
      value("--approve-cost-sha256"), Number(value("--budget-usd")), process.env);
    process.stderr.write(`Reviewed estimated maximum: USD ${cost.estimatedMaximumUSD}; ceiling USD ${cost.budgetUSD}; ${requests} requests, no retries.\n`);
  }
  if (args.includes("--plan")) {
    process.stdout.write(`${JSON.stringify({ configuration: config, requests, cost: cost ?? { estimatedMaximumUSD: 0, note: "Local double; no provider requests." } }, null, 2)}\n`);
    return;
  }
  const generate = config.mode === "live-model" ? (input: Parameters<typeof generateEvaluationResponse>[0]) =>
    generateEvaluationResponse(input, `${config.provider}/${config.model}` as `${string}/${string}`,
      { maxOutputTokens: config.maxOutputTokens, temperature: config.temperature }) : undefined;
  const report = await runModelEvaluation(config, decisionScenarios(), generate);
  process.stdout.write(`${JSON.stringify({ ...report, cost: cost ?? { estimatedMaximumUSD: 0, note: "Local double only." } }, null, 2)}\n`);
  process.stderr.write(`${summarizeModel(report)}\n`);
  process.exitCode = report.totals.passed === report.totals.trials ? 0 : 1;
}
