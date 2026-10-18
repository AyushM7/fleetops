import { foundationCases, foundationDouble } from "./benchmark.js";
import { runEvaluation, summarize } from "./index.js";
import { dataset } from "./dataset.js";
import { runDataset, summarizeDataset } from "./dataset-runner.js";
import { adversarialCases } from "./adversarial.js";
import { runAdversarial, summarizeAdversarial } from "./adversarial-runner.js";
import { runRecovery, summarizeRecovery } from "./recovery.js";
import { modelCLI } from "./model-cli.js";
import { runDecisionEvaluation } from "./decisions.js";

if (process.argv.includes("--live") && !process.argv.includes("--model-eval")) {
  throw new Error("Live flag requires explicit --model-eval mode.");
}
if (process.argv.includes("--decision-eval")) {
  if (process.argv.slice(2).some(arg => arg !== "--decision-eval")) throw new Error("Decision evaluation permits only offline --decision-eval.");
  const report = await runDecisionEvaluation();
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`Decision v2 scripted validation: ${report.totals.passed}/${report.totals.cases}; actual LLM accuracy and simulator benefit not measured.\n`);
  process.exitCode = report.totals.failed === 0 ? 0 : 1;
} else if (process.argv.includes("--model-eval")) {
  await modelCLI(process.argv.slice(2));
} else if (process.argv.includes("--foundation")) {
  const report = await runEvaluation(foundationCases(), foundationDouble);
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarize(report)}\n`);
  process.exitCode = report.totals.cases > 0 && report.totals.unsuccessful === 0 ? 0 : 1;
} else if (process.argv.includes("--recovery")) {
  const report = await runRecovery();
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeRecovery(report)}\n`);
  process.exitCode = report.totals.experiments > 0 && report.totals.failed === 0 ? 0 : 1;
} else if (process.argv.includes("--adversarial")) {
  const report = await runAdversarial(adversarialCases());
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeAdversarial(report)}\n`);
  process.exitCode = report.totals.cases > 0 && report.totals.defenseFailures === 0 ? 0 : 1;
} else {
  const report = await runDataset(dataset());
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeDataset(report)}\n`);
  process.exitCode = report.scenarios.length > 0 && report.totals.expectationMismatches === 0 ? 0 : 1;
}
