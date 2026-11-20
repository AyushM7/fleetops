import { singleOperationsFetch } from "./operations-fetch.js";

async function run() {
  if (!process.env.OPENAI_API_KEY?.trim() || !/^[1-9][0-9]{0,18}$/.test(process.env.FLEETOPS_OPERATIONS_RESERVATION ?? "")) {
    throw new Error("Operations reservation required.");
  }
  globalThis.fetch = singleOperationsFetch(globalThis.fetch);
  const { makeOpenAIOperationsDecision } = await import("./operations-manager.js");
  let observation = "";
  process.stdin.setEncoding("utf8");
  for await (const chunk of process.stdin) {
    observation += chunk;
    if (Buffer.byteLength(observation) > 48 * 1024) throw new Error("Operations observation too large.");
  }
  let invoked = false;
  const decision = await makeOpenAIOperationsDecision(observation, () => {
    if (invoked) throw new Error("Operations model already invoked.");
    invoked = true;
  }, AbortSignal.timeout(7_000));
  process.stdout.write(JSON.stringify(decision));
}

void run().catch(() => { process.exitCode = 1; });
