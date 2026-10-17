# P4-01 dependency audit investigation

Investigated 2026-10-16 using the existing P4-00 lockfile. No dependency versions,
overrides or lockfile entries were changed in P4-01. No `audit fix` was run.

`npm audit --json` reports **five moderate findings, zero high or critical**.
`npm audit --omit=dev --json` reports the same five. They are one underlying
advisory propagated through this production dependency chain:

```text
@fleetops/agent
└─ @mastra/core@1.75.0
   └─ gray-matter@4.0.3
      └─ js-yaml@3.15.2
         └─ argparse@1.0.10
            └─ sprintf-js@1.0.3
```

| Affected package | Installed version | Dependency classification | Patch or alternative verified |
| --- | --- | --- | --- |
| `@mastra/core` | 1.75.0 | Direct runtime | Stable 1.78.0 still depends on `gray-matter ^4.0.3`; upgrading to it does not remove this path |
| `gray-matter` | 4.0.3 | Transitive runtime | Latest 4.0.3 still requires `js-yaml ^3.13.1`; no parent release removing this path was found |
| `js-yaml` | 3.15.2 | Transitive runtime | Latest 3.x is still affected through argparse; 4.1.0 uses `argparse ^2.0.1`, and latest 5.5.0 uses `argparse ^3.0.3`. These are major-version alternatives, not compatible patches for gray-matter's current range |
| `argparse` | 1.0.10 | Transitive runtime | 2.0.1 and latest 3.0.3 have no sprintf-js dependency; a major-version substitution would require parent compatibility review |
| `sprintf-js` | 1.0.3 | Transitive runtime | No published patch. Latest 1.1.3 remains in the affected range |

The [GitHub advisory GHSA-hp3w-g68c-fv3c / CVE-2026-97058](https://github.com/advisories/GHSA-hp3w-g68c-fv3c)
describes a denial of service when an attacker controls a sprintf format string
with excessive numeric precision. Versions through 1.1.3 are affected and the
advisory lists no patched version. These are not five independent exploits.

The audit's suggested `@mastra/core@1.0.4` is a **downgrade**, not a patch to the
installed release. Its metadata has no gray-matter dependency, but compatibility
with this integration has not been established. Neither that downgrade nor a
forced transitive major upgrade was performed. Remediation needs a separately
approved, compatibility-tested dependency change or an upstream release.

## Application execution path

This is a production install finding, **not development-only**. TypeScript,
Vitest and Node types are development dependencies but are not the source of
these five findings. `npm ci --omit=dev` alone does not remove the vulnerable chain.

Static inspection of the installed packages found:

- Mastra's `agent-skills-resolver-*.js` and `workspace-skills-*.js` import
  `gray-matter` for Markdown/frontmatter skills processing.
- `gray-matter` uses js-yaml's library API. `js-yaml/index.js` loads
  `lib/js-yaml.js`; its library files do not load argparse.
- `js-yaml/bin/js-yaml.js`, the separate YAML CLI, loads argparse. Argparse's
  argument/help formatting loads `sprintf-js`.
- FleetOps configures no workspace or Mastra skills, accepts no frontmatter or
  sprintf format strings, and invokes no YAML CLI. The model has only the combined
  fleet read and bounded SetCharging proposal tools. Neither tool invokes a shell.

The inference from this source trace is that the vulnerable **sprintf execution
path is not reachable through the current FleetOps analysis/proposal tools**.
This is a scoped reachability assessment, not a claim that the installed package
is patched or safe for every Mastra feature. Enabling skills, CLI processing,
arbitrary plugins or shell tools would require a fresh assessment. The Go server
does not run npm dependencies; the dashboard's separate package manifest does not
depend on the agent or Mastra.

## Reproduce the investigation (read-only)

```powershell
npm audit --json
npm audit --omit=dev --json
npm ls @mastra/core gray-matter js-yaml argparse sprintf-js --all
npm view sprintf-js versions --json
npm view @mastra/core dist-tags.latest
npm view @mastra/core@1.78.0 version dependencies.gray-matter --json
npm view @mastra/core@1.0.4 version dependencies.gray-matter --json
npm view gray-matter version dependencies --json
npm view js-yaml@3 version --json
npm view js-yaml@4.1.0 version dependencies --json
npm view js-yaml version dependencies --json
npm view argparse@2.0.1 version dependencies --json
npm view argparse version dependencies --json
```

Both audit commands intentionally exit **1** while these findings remain.
Version data comes from the [npm registry](https://registry.npmjs.org/), with the
installed manifests and source files used for dependency/path tracing. The audit
does not make a model API request.
