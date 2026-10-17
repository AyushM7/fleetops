import { runLive, runOffline } from "./agent.js";
import { readConfig } from "./config.js";
import { createFleetClient, FleetError } from "./graphql.js";

async function main(): Promise<void> {
  const config = readConfig(process.env, process.argv.slice(2));
  const api = createFleetClient(config.endpoint, config.token);
  if (config.live) console.error("Live model mode sends fleet data to the configured provider and may incur charges (maximum four model steps).");
  const result = config.live
    ? await runLive(api, { enabled: true, model: config.model!, allowSubmit: config.allowSubmit, demo: config.demo })
    : await runOffline(api, { allowSubmit: config.allowSubmit, demo: config.demo });
  console.log(JSON.stringify(result, null, 2));
  if (["rejected", "unknown"].includes(result.proposal.status)) process.exitCode = 1;
}
main().catch((error: unknown) => {
  console.error(error instanceof FleetError ? error.message : "Agent failed. Check private configuration, provider opt-in, and backend availability. No automatic retry was made.");
  process.exitCode = 1;
});
