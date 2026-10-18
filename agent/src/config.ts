import { decisionObjectiveSchema, type DecisionObjective } from "./decision-contracts.js";

export type RunConfig = {
  endpoint: string; token: string; live: boolean; allowSubmit: boolean;
  model?: `${string}/${string}`; demo?: { robotId: string; stationId: string };
  decisionObjective?: DecisionObjective;
};
export function readConfig(env: NodeJS.ProcessEnv, args: string[]): RunConfig {
  if (args.some(a => !["--live", "--submit", "--demo", "--decision-v2"].includes(a))) throw new Error("Unknown CLI option.");
  const live = args.includes("--live");
  const model = env.FLEETOPS_MODEL;
  if (!env.FLEETOPS_PROPOSER_TOKEN) throw new Error("Configure a private FLEETOPS_PROPOSER_TOKEN with backend proposer role.");
  if (live && (!model || !/^[a-z0-9-]+\/[a-zA-Z0-9._:/-]+$/.test(model))) throw new Error("Configure FLEETOPS_MODEL as provider/model for live execution.");
  if (args.includes("--demo") && (!env.FLEETOPS_DEMO_ROBOT_ID || !env.FLEETOPS_DEMO_STATION_ID)) throw new Error("Demo requires explicit robot and station IDs.");
  if (args.includes("--decision-v2") && (!live || args.includes("--demo"))) throw new Error("Decision v2 requires --live and excludes the charging demonstration.");
  const objective = args.includes("--decision-v2") ? decisionObjectiveSchema.safeParse(env.FLEETOPS_DECISION_OBJECTIVE ?? "compare_tradeoffs") : undefined;
  if (objective && !objective.success) throw new Error("Invalid decision objective.");
  return {
    endpoint: env.FLEETOPS_GRAPHQL_URL ?? "http://127.0.0.1:8080/graphql", token: env.FLEETOPS_PROPOSER_TOKEN,
    live, allowSubmit: args.includes("--submit"), model: model as RunConfig["model"],
    demo: args.includes("--demo") ? { robotId: env.FLEETOPS_DEMO_ROBOT_ID!, stationId: env.FLEETOPS_DEMO_STATION_ID! } : undefined,
    ...(objective?.success ? { decisionObjective: objective.data } : {}),
  };
}
export function hasProviderKey(model: string, env: NodeJS.ProcessEnv): boolean {
  const keys: Record<string, string> = { openai: "OPENAI_API_KEY", anthropic: "ANTHROPIC_API_KEY", google: "GOOGLE_GENERATIVE_AI_API_KEY" };
  const key = keys[model.split("/")[0]];
  return !!key && !!env[key]?.trim();
}
