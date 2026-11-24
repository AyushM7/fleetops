if (process.argv.includes("--live") && !process.argv.includes("--model-eval")) {
  throw new Error("Live flag requires explicit --model-eval mode.");
}
if (process.argv.includes("--decision-eval")) {
  if (process.argv.slice(2).some(arg => arg !== "--decision-eval")) throw new Error("Decision evaluation permits only offline --decision-eval.");
  const { runDecisionEvaluation } = await import("./decisions.js");
  const report = await runDecisionEvaluation();
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`Decision v2 scripted validation: ${report.totals.passed}/${report.totals.cases}; actual LLM accuracy and simulator benefit not measured.\n`);
  process.exitCode = report.totals.failed === 0 ? 0 : 1;
} else if (process.argv.includes("--model-eval")) {
  const { modelCLI } = await import("./model-cli.js");
  await modelCLI(process.argv.slice(2));
} else if (process.argv.includes("--foundation")) {
  const { foundationCases, foundationDouble } = await import("./benchmark.js");
  const { runEvaluation, summarize } = await import("./index.js");
  const report = await runEvaluation(foundationCases(), foundationDouble);
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarize(report)}\n`);
  process.exitCode = report.totals.cases > 0 && report.totals.unsuccessful === 0 ? 0 : 1;
} else if (process.argv.includes("--recovery")) {
  const { runRecovery, summarizeRecovery } = await import("./recovery.js");
  const report = await runRecovery();
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeRecovery(report)}\n`);
  process.exitCode = report.totals.experiments > 0 && report.totals.failed === 0 ? 0 : 1;
} else if (process.argv.includes("--adversarial")) {
  const { adversarialCases } = await import("./adversarial.js");
  const { runAdversarial, summarizeAdversarial } = await import("./adversarial-runner.js");
  const report = await runAdversarial(adversarialCases());
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeAdversarial(report)}\n`);
  process.exitCode = report.totals.cases > 0 && report.totals.defenseFailures === 0 ? 0 : 1;
} else {
  const { dataset } = await import("./dataset.js");
  const { runDataset, summarizeDataset } = await import("./dataset-runner.js");
  const report = await runDataset(dataset());
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  process.stderr.write(`${summarizeDataset(report)}\n`);
  process.exitCode = report.scenarios.length > 0 && report.totals.expectationMismatches === 0 ? 0 : 1;
}
