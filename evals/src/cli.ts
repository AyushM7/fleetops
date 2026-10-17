import { foundationCases, foundationDouble } from "./benchmark.js";
import { runEvaluation, summarize } from "./index.js";
import { dataset } from "./dataset.js";
import { runDataset, summarizeDataset } from "./dataset-runner.js";

if (process.argv.includes("--foundation")) {
  const report = await runEvaluation(foundationCases(), foundationDouble);
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarize(report)}\n`);
  process.exitCode = report.totals.cases > 0 && report.totals.unsuccessful === 0 ? 0 : 1;
} else {
  const report = await runDataset(dataset());
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeDataset(report)}\n`);
  process.exitCode = report.scenarios.length > 0 && report.totals.expectationMismatches === 0 ? 0 : 1;
}
