import { existsSync } from "node:fs";
import { registerHooks } from "node:module";

// Source files retain the agent's NodeNext .js imports; Node 24 strips TS types.
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith(".") && specifier.endsWith(".js") && context.parentURL) {
      const source = new URL(specifier.slice(0, -3) + ".ts", context.parentURL);
      if (existsSync(source)) return nextResolve(source.href, context);
    }
    return nextResolve(specifier, context);
  },
});
await import("../src/cli.ts");
