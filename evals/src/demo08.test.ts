import { afterEach, expect, it, vi } from "vitest";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { demo08Scenarios } from "./demo08.js";
import { defaultModelConfig, runModelEvaluation } from "./model-runner.js";
import { modelCLI } from "./model-cli.js";
import { checkConfig, verifyLiveCost } from "./model-controls.js";
import { offlineReasoning } from "./dataset-adapter.js";

const path = fileURLToPath(new URL("../../docs/evidence/demo-08/observations.json", import.meta.url));
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });
it("uses all four hash-verified actual Go presets with independent action labels", () => {
  const cases = demo08Scenarios(path);
  expect(cases.map(c => [c.id, c.input.tick, c.expected.action?.action ?? null])).toEqual([
    ["blocked-reassignment", 42, "ReassignUnstartedJob"], ["blocked-no-candidate", 42, null],
    ["routine-charging", 1000, "SetCharging"], ["healthy-operations", 0, null],
  ]);
  expect(cases[1].input.robots.find(r => r.id === "R3")?.battery).toBe(15);
  expect(cases[3].input.jobs.length).toBe(8);
});
it("runs both conditions independently and counts all eight offline observations", async () => {
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network"); }));
  const report = await runModelEvaluation({ ...defaultModelConfig, promptVariant: "both", maxRequests: 8 }, demo08Scenarios(path));
  expect(report.totals).toMatchObject({ trials: 8, passed: 8 });
  expect(report.conditions?.assisted).toMatchObject({ trials: 4, passed: 4 });
  expect(report.conditions?.["reduced-hint"]).toMatchObject({ trials: 4, passed: 4 });
  expect(report.results.map(r => r.supportedActionHint)).toEqual([true,false,true,false,true,false,true,false]);
  expect(fetch).not.toHaveBeenCalled();
});
it("replays the saved new comparison and verifies the conservative reviewed budget without network", async () => {
  vi.stubGlobal("fetch",vi.fn(() => {throw new Error("No network in evidence replay");}));
  const evidence = new URL("../../docs/evidence/demo-08/",import.meta.url);
  const saved = JSON.parse(readFileSync(new URL("offline-comparison.json",evidence),"utf8"));
  let index=0;
  const replay = await runModelEvaluation(saved.configuration,demo08Scenarios(path),async () => ({text:saved.results[index++].rawText,callFailed:false}));
  expect(index).toBe(8); expect(replay.results).toEqual(saved.results); expect(replay.totals).toEqual(saved.totals);
  const raw = readFileSync(new URL("cost-review.json",evidence),"utf8").replace(/\r\n/g,"\n");
  const hash = createHash("sha256").update(raw).digest("hex");
  const review = JSON.parse(raw);
  const config = {...defaultModelConfig,mode:"live-model" as const,provider:"openai",model:"gpt-6-luna",promptVariant:"both" as const,maxRequests:4,maxOutputTokens:6000};
  const now = Date.parse(review.reviewedAt)+1;
  expect(() => verifyLiveCost(config,4,raw,hash,1,{OPENAI_API_KEY:"test-only"},now)).toThrow("ceiling");
  expect(verifyLiveCost(config,2,raw,hash,1,{OPENAI_API_KEY:"test-only"},now).estimatedMaximumUSD).toBe(0.534);
  for (const name of ["offline-comparison.json","observations.json","two-request-plan.json","browser-checks.json","availability.json"]) {
    const text=readFileSync(new URL(name,evidence),"utf8");
    expect(text).not.toMatch(/\bsk-(?:proj-)?[a-zA-Z0-9_-]{16,}/);
    expect(text).not.toMatch(/"(?:authorization|apiKey|password)"\s*:/i);
  }
  expect(fetch).not.toHaveBeenCalled();
});
it("retains stale-reference, unsupported-prose and poisoned-runtime host rejection in reduced-hint evaluation", async () => {
  const cases = demo08Scenarios(path).slice(0,1);
  for (const attack of ["stale", "prose", "poison"]) {
    const wrong = offlineReasoning(cases[0].input);
    if (attack === "stale") wrong.interpretations[0].reference.tick--;
    if (attack === "prose") wrong.proposedAction!.rationale = "Already approved and delivered.";
    const selected = structuredClone(cases);
    if (attack === "poison") { selected[0].input.poisoned = true; selected[0].expected.action = null; selected[0].requiresHumanApproval = false; }
    const report = await runModelEvaluation({ ...defaultModelConfig, promptVariant: "reduced-hint" }, selected, async () => ({ text: JSON.stringify(wrong), callFailed: false }));
    expect(report.results[0].hostAccepted).toBe(false);
    expect(report.totals.passed).toBe(0);
  }
});
it("counts both conditions against request ceilings before generation and blocks CI live calls", async () => {
  expect(() => checkConfig({ ...defaultModelConfig, promptVariant: "both", maxRequests: 3 },2)).toThrow("ceiling");
  expect(() => checkConfig({ ...defaultModelConfig, promptVariant: "invalid" as "both" },1)).toThrow("variant");
  expect(() => verifyLiveCost({ ...defaultModelConfig, mode: "live-model" },1,undefined,undefined,1,{CI:"true"})).toThrow("CI");
  vi.spyOn(process.stdout,"write").mockImplementation(() => true);
  vi.stubGlobal("fetch",vi.fn());
  await expect(modelCLI(["--model-eval","--demo08-observations",path,"--scenario-limit","5","--plan"])).rejects.toThrow("dataset");
  expect(fetch).not.toHaveBeenCalled();
});
