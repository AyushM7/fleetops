import type { Config } from "./agent07-cost.js";
import { RequestReserve } from "./agent07-cost.js";

export function liveTransport(config: Config, endpoint: string, reserve: RequestReserve, network: typeof fetch, signal: AbortSignal, contextWindowTokens: number) {
  let permits = 0, failed = false, failedHTTPResponses=0, uncertainHTTPResults=0;
  return { get measurements() { return { providerHTTPAttempts:reserve.calls, failedHTTPResponses, uncertainHTTPResults }; }, permit() { if (failed || permits) throw new Error("Concurrent or failed generation cannot retry."); permits++; },
    fetch: (async (input, init) => {
      const url = new URL(input instanceof Request ? input.url : String(input));
      if (url.href === endpoint) return network(input, { ...init, redirect: "error", signal: AbortSignal.any([signal, ...(init?.signal ? [init.signal] : [])]) });
      if (url.href !== "https://api.openai.com/v1/responses" || !permits || failed || init?.method !== "POST" || typeof init.body !== "string") throw new Error("Unreviewed provider transport blocked.");
      permits--;
      let sent=false;
      try {
        const body = JSON.parse(init.body) as Record<string, unknown>;
        const tools = body.tools as { type: string; name: string }[] | undefined;
        if (body.model !== config.model || !Number.isSafeInteger(body.max_output_tokens) || (body.max_output_tokens as number) < 1 ||
          (body.max_output_tokens as number) > config.maxOutputTokens || body.store !== false || body.service_tier !== "default" ||
          !Number.isSafeInteger(contextWindowTokens) || Buffer.byteLength(init.body)+config.maxOutputTokens > contextWindowTokens ||
          tools?.some(t => t.type !== "function" || !["readIncident", "readRobots", "readJobs", "listRecoveryCandidates"].includes(t.name))) throw new Error("Provider request differs from reviewed bounds.");
        reserve.take();
        sent=true;
        const response = await network(input, { ...init, redirect: "error", signal: AbortSignal.any([signal, ...(init.signal ? [init.signal] : [])]) });
        if (!response.ok) { failed = true; failedHTTPResponses++; }
        return response;
      } catch { failed = true; if(sent) uncertainHTTPResults++; throw new Error("Provider result unavailable; reserved request remains charged; no retry."); }
    }) as typeof fetch };
}
