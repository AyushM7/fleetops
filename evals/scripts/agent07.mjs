import { existsSync } from "node:fs";
import { registerHooks, stripTypeScriptTypes } from "node:module";
registerHooks({ resolve(specifier, context, next) {
  if (specifier.startsWith(".") && specifier.endsWith(".js") && context.parentURL) {
    const source = new URL(specifier.slice(0, -3)+".ts", context.parentURL);
    if (existsSync(source)) return next(source.href, context);
  }
  return next(specifier, context);
}, load(url, context, next) {
  const loaded = next(url, context);
  if (url.endsWith(".ts") && !url.includes("/node_modules/")) return { format: "module", source: stripTypeScriptTypes(String(loaded.source), { mode: "transform", sourceUrl: url }) };
  return loaded;
} });
const { agent07CLI, fixtureTrial } = await import("../src/agent07-cli.ts");
try { if (process.argv.length === 3 && process.argv[2] === "--fixture-trial") await fixtureTrial(); else await agent07CLI(process.argv.slice(2)); }
catch (error) { process.stderr.write(`${error instanceof Error && !process.argv.includes("--fixture-trial") ? error.message : "Agent07 trial stopped safely; no retry; raw errors suppressed."}\n`); process.exitCode = 1; }
