import { afterEach, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { mkdtempSync, rmdirSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { modelCLI, parseModelArgs } from "./model-cli.js";
import { decisionScenarios } from "./model-runner.js";

const originalExitCode = process.exitCode;
afterEach(() => {
  vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.unstubAllEnvs();
  process.exitCode = originalExitCode;
});
function captureCLI() {
  const chunks: string[] = [];
  vi.spyOn(process.stdout, "write").mockImplementation(chunk => { chunks.push(String(chunk)); return true; });
  vi.spyOn(process.stderr, "write").mockImplementation(() => true);
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("No network permitted."); }));
  return () => JSON.parse(chunks.join(""));
}
it("defaults to all scenarios and identifies them in the dry run", async () => {
  const report = captureCLI();
  await modelCLI(["--model-eval", "--plan"]);
  expect(report()).toMatchObject({ requests: 12, selectedScenarioCount: 12,
    selectedScenarioIds: decisionScenarios().map(s => s.id) });
  expect(fetch).not.toHaveBeenCalled();
});
it("selects the first two scenarios in dataset order, with requests based on selected cases and trials", async () => {
  const report = captureCLI();
  await modelCLI(["--model-eval", "--scenario-limit", "2", "--trials", "3", "--max-requests", "6", "--plan"]);
  expect(report()).toMatchObject({ requests: 6, selectedScenarioCount: 2,
    selectedScenarioIds: ["traffic-eligible", "traffic-no-candidate"] });
  expect(fetch).not.toHaveBeenCalled();
});
it("runs only the selected scenarios and their trials through the offline model", async () => {
  const report = captureCLI();
  await modelCLI(["--model-eval", "--scenario-limit", "2", "--trials", "2", "--max-requests", "4"]);
  expect(report()).toMatchObject({ selectedScenarioCount: 2, totals: { trials: 4, passed: 4 } });
  expect(report().results.map((r: { id: string; trial: number }) => [r.id, r.trial])).toEqual([
    ["traffic-eligible", 1], ["traffic-eligible", 2], ["traffic-no-candidate", 1], ["traffic-no-candidate", 2],
  ]);
  expect(report().execution).toBe("not_measured");
  expect(fetch).not.toHaveBeenCalled();
});
it.each(["0", "-1", "1.5", "NaN", "Infinity", "invalid", "", " ", "2e0", "9007199254740992", "13"])("rejects invalid scenario limit %j before generation", async limit => {
  captureCLI();
  await expect(modelCLI(["--model-eval", "--scenario-limit", limit, "--plan"])).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
});
it("accepts the available upper bound and rejects missing/duplicate limit options", () => {
  expect(parseModelArgs(["--scenario-limit", String(decisionScenarios().length)]).scenarioLimit).toBe(12);
  expect(() => parseModelArgs(["--scenario-limit"])).toThrow();
  expect(() => parseModelArgs(["--scenario-limit", "2", "--scenario-limit", "1"])).toThrow();
});
it("rejects a request ceiling below selected cases times trials", async () => {
  captureCLI();
  await expect(modelCLI(["--model-eval", "--scenario-limit", "2", "--trials", "3", "--max-requests", "5", "--plan"])).rejects.toThrow("ceiling");
  expect(fetch).not.toHaveBeenCalled();
});
it("retains live configuration and cost gates when selection is limited", async () => {
  captureCLI();
  await expect(modelCLI(["--model-eval", "--scenario-limit", "2", "--live", "--plan"])).rejects.toThrow("provider and model");
  await expect(modelCLI(["--model-eval", "--scenario-limit", "2", "--live", "--provider", "openai", "--model", "test-only", "--plan"])).rejects.toThrow("Pricing unavailable");
  expect(fetch).not.toHaveBeenCalled();
});
it("scales dry-run cost/budget checks to selected trials, retaining credential checks without generation", async () => {
  const report = captureCLI();
  vi.stubEnv("OPENAI_API_KEY", "test-only-never-transmitted");
  const directory = mkdtempSync(join(tmpdir(), "fleetops-scenario-limit-"));
  const file = join(directory, "review.json");
  try {
    const review = JSON.stringify({ provider: "openai", model: "test-only", source: "Synthetic test prices; not a real quote",
      reviewedAt: "2000-01-01", expiresAt: "2099-01-01", maxBillableInputTokens: 100000,
      inputUSDPerMillion: 10, outputUSDPerMillion: 20, outputLimitIncludesAllBilledTokens: true });
    writeFileSync(file, review);
    const hash = createHash("sha256").update(review).digest("hex");
    const args = ["--model-eval", "--live", "--provider", "openai", "--model", "test-only", "--scenario-limit", "2",
      "--trials", "2", "--max-requests", "4", "--cost-review", file, "--approve-cost-sha256", hash, "--plan"];
    await modelCLI([...args, "--budget-usd", "5"]);
    expect(report()).toMatchObject({ requests: 4, selectedScenarioCount: 2,
      selectedScenarioIds: ["traffic-eligible", "traffic-no-candidate"] });
    expect(report().cost.estimatedMaximumUSD).toBeCloseTo(4.192);
    await expect(modelCLI([...args, "--budget-usd", "4"])).rejects.toThrow("spending ceiling");
    vi.stubEnv("OPENAI_API_KEY", "");
    await expect(modelCLI([...args, "--budget-usd", "5"])).rejects.toThrow("credential missing");
    expect(fetch).not.toHaveBeenCalled();
  } finally { unlinkSync(file); rmdirSync(directory); }
});
