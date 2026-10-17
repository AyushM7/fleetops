import { foundationCases, foundationDouble } from "./benchmark.js";
import { runEvaluation, summarize } from "./index.js";

const report = await runEvaluation(foundationCases(), foundationDouble);
process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
process.stderr.write(`${summarize(report)}\n`);
process.exitCode = report.totals.cases > 0 && report.totals.unsuccessful === 0 ? 0 : 1;
