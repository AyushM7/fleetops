# Decisions

Placeholder for future application decisions; setup and health-server decisions are recorded below.

## P0-01: setup versions

Verified on 2026-10-13 (America/Chicago). The 14-day rule requires a release to be at least 14 days old on the selection date, giving a cutoff of 2026-09-29.

### Node.js

- Version: `24.21.0`, pinned in `.nvmrc`.
- Release date: 2026-09-08; age at selection: 35 days.
- Status: LTS (Krypton), confirmed by both official sources.
- Sources: [official downloads](https://nodejs.org/en/download) and [24.21.0 release](https://nodejs.org/en/blog/release/v24.21.0).
- Selection reason: the requested exact version is an officially listed LTS release and exceeds the 14-day minimum. This pin does not install or change the host runtime.

### PostgreSQL

- Version: `18.6`; exact Docker Official Image tag: `postgres:18.6`.
- Release date: 2026-08-13; age at selection: 61 days.
- Sources: [official image tags](https://hub.docker.com/_/postgres/tags?name=18) and [18.6 release notes](https://www.postgresql.org/docs/release/18.6/).
- Selection reason: the requested exact tag is listed and the PostgreSQL release exceeds the 14-day minimum. Release age refers to the PostgreSQL release date, not the image rebuild date.
- Volume decision: mount `postgres_data` at `/var/lib/postgresql`. PostgreSQL 18 uses the version-specific default data directory `/var/lib/postgresql/18/docker`; do not mount at `/var/lib/postgresql/data`. See the [official image storage documentation](https://hub.docker.com/_/postgres).
- Local access: bind to loopback only, require all three database environment variables, and preserve the named volume across `docker compose down`.

## P0-02: Go health server

- Approved version: `Go 1.27.1`, released 2026-09-01.
- Official source: [Go release history](https://go.dev/doc/devel/release).
- Selection date: 2026-10-13 (America/Chicago); release age: 42 days. This exceeds the 14-day minimum (cutoff 2026-09-29). Go 1.27.2 was released 2026-10-08 and is only five days old, so 1.27.1 is the newest eligible release in that series.
- Verification exception: `https://go.dev/dl/` was inaccessible and was not independently verified. The owner explicitly authorized the official release history as the sole verification source and approved Go 1.27.1 for P0-02.
- Module: `github.com/AyushM7/fleetops/backend`; `go.mod` pins `go 1.27.1`. No `toolchain` directive is present. The Go directive sets the minimum required version; validation uses the exact approved toolchain.
- Dependencies: Go standard library only.
- HTTP: `PORT` defaults to 8080 when empty or unset; otherwise require decimal digits and a value from 1 through 65535. `/healthz` accepts only GET and returns exact JSON without a trailing newline.
- Lifecycle: SIGINT and SIGTERM cancel a signal context. Header reads and graceful shutdown each have a five-second timeout. Shutdown uses a fresh context so the signal cannot cancel request draining; unexpected serving or shutdown errors return a nonzero process status.

## P0-03: independent TypeScript packages

- Selection date: 2026-10-13 (America/Chicago); 14-day cutoff: 2026-09-29.
- Runtime: Node `24.21.0`, read from `.nvmrc` and verified inside `node:24.21.0` before npm installation. The host's `24.16.0` is not used. Node's 2026-09-08 release and official sources are recorded under P0-01 above.
- TypeScript: `7.0.2`, released 2026-08-20 (54 days old). This stable release is eligible and listed as latest in the [official releases](https://github.com/microsoft/TypeScript/releases) and [7.0.2 release](https://github.com/microsoft/TypeScript/releases/tag/v7.0.2). The [TypeScript site](https://www.typescriptlang.org/) and [official 7.0 announcement](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/) confirm npm installation provides the `tsc` command.
- Vitest: `5.0.2`, released 2026-09-25 (18 days old), verified through the [official releases](https://github.com/vitest-dev/vitest/releases) and [5.0.2 release](https://github.com/vitest-dev/vitest/releases/tag/v5.0.2). It is the newest stable release meeting the cutoff; 5.0.3 (September 30) and 5.0.4 (October 12) are too recent.
- Compatibility: published npm metadata reports TypeScript's Node range as `>=16.20.0` and Vitest's as `^22.12.0 || ^24.0.0 || >=26.0.0`; Node 24.21.0 satisfies both. The [Vitest site](https://vitest.dev/) and [getting-started guide](https://vitest.dev/guide/) document TypeScript support and Node >=22.12.0 with Vite >=6.4.0. Vitest 5.0.2 declares Vite `^6.4.0 || ^7.0.0 || ^8.0.0` as a peer; npm resolves its dependency tree without adding Vite as a direct dependency.
- Both private ESM packages use only exact `typescript` and `vitest` devDependencies, installed with `npm install --save-dev --save-exact typescript@7.0.2 vitest@5.0.2`. There are no runtime dependencies or workspaces; npm independently generates each complete lockfile.
- Both packages use strict, no-emit ES2022/NodeNext configuration and include all of `src`, including tests. Local imports use `.js` extensions for NodeNext compatibility. Exported helpers are trivial setup checks, not agent tools or evaluation logic.
- Validation used npm `11.19.0` bundled with Node 24.21.0 in Docker/Linux. Both packages passed `npm ci`, one Vitest test, `tsc --noEmit`, and direct dependency listings. After deleting only each package's `node_modules`, a second `npm ci` passed from the existing lockfile.
- No `skipLibCheck` setting was added: both initial typechecks passed. No `@types/node` dependency was installed or required.

## P0-04: minimal React application

- Selection date: 2026-10-13 (America/Chicago); eligible releases must be at least 14 days old, with a 2026-09-29 cutoff.
- Runtime: Node `24.21.0` from `.nvmrc`, verified in Docker before installation; npm `11.19.0`. The mismatched Windows host runtime is not used.
- React and React DOM: `19.3.0`, released 2026-09-09 (34 days old). Sources: [official React release](https://github.com/react/react/releases/tag/v19.3.0), [React versions](https://react.dev/versions), and publisher metadata for [react](https://registry.npmjs.org/react) and [react-dom](https://registry.npmjs.org/react-dom). Matching versions satisfy React DOM's `react: ^19.3.0` peer requirement.
- React definitions: `@types/react@19.3.0` and `@types/react-dom@19.3.0`, both published 2026-09-09 (34 days old). Publication dates and peer requirements come from the DefinitelyTyped packages' official npm metadata: [@types/react](https://registry.npmjs.org/@types%2Freact) and [@types/react-dom](https://registry.npmjs.org/@types%2Freact-dom), maintained in [DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped). React DOM types require `@types/react: ^19.3.0`; both advertise a TypeScript 5.6 minimum, satisfied by 7.0.2.
- Vite: `8.3.1`, released 2026-09-24 (19 days old). Sources: [official release](https://github.com/vitejs/vite/releases/tag/v8.3.1), [npm metadata](https://registry.npmjs.org/vite), and [Vite guide](https://vite.dev/guide/). It is the newest stable version meeting the cutoff; the newer 8.3.x releases are too recent.
- React plugin: `@vitejs/plugin-react@6.1.1`, released 2026-08-28 (46 days old). Sources: [official release](https://github.com/vitejs/vite-plugin-react/releases/tag/plugin-react%406.1.1) and [npm metadata](https://registry.npmjs.org/@vitejs%2Fplugin-react). Version 6.1.2 was released October 5 and is too recent. The selected plugin requires Vite `^8.0.0`, satisfied by 8.3.1.
- Compatibility: Vite and the React plugin require Node `^20.19.0 || >=22.12.0`, which includes 24.21.0. Vitest 5.0.2 accepts Vite `^6.4.0 || ^7.0.0 || ^8.0.0`; the chosen Vite satisfies both Vitest and the plugin. Only the ticket's two runtime and six development dependencies are declared directly; all use exact pins and npm-generated locking.
- TypeScript `7.0.2` and Vitest `5.0.2` are reused unchanged from both P0-03 manifests; their release dates and verification sources are recorded above. No independent upgrade was performed.
- Scope: the app mounts into `#root` and displays only the project name and status. Strict ES2022/Bundler configuration includes source, tests, and Vite config. Vitest runs in Node and tests server-rendered HTML without browser APIs or jsdom.
- Declaration-check exception: initial `npm run typecheck` failed only in third-party declarations (Vite, Vitest, Rolldown, and obug), including missing Node types and `Symbol.asyncDispose`. Following P0-04 requirement 15, `skipLibCheck: true` was added; strict checking remains enabled for application code, tests, and Vite config. No `@types/node` was installed.
- Validation: the Node-environment rendering test passed, followed by successful typecheck and production build after the declaration-check exception. Initial and clean `npm ci` both passed without force, legacy peer flags, or peer-dependency errors. The dev server returned HTTP 200 on 127.0.0.1:5173; its dedicated process group was stopped with SIGTERM (wait status 143), and the subsequent curl failed to connect (exit 7), confirming shutdown.

## P0-05: root checks and CI

- Selection date: 2026-10-13; the 14-day cutoff is 2026-09-29. Official GitHub release API records and tag references independently confirmed all pins below; no candidate SHA discrepancies were found. The sandbox could not resolve GitHub's API, so verification used network-enabled execution.
- `actions/checkout@v7.0.1`: `3d3c42e5aac5ba805825da76410c181273ba90b1`, released 2026-07-20 (85 days old). Sources: [release](https://github.com/actions/checkout/releases/tag/v7.0.1), [release metadata](https://api.github.com/repos/actions/checkout/releases/tags/v7.0.1), [tag reference](https://api.github.com/repos/actions/checkout/git/ref/tags/v7.0.1).
- `actions/setup-go@v7.0.0`: `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`, released 2026-07-16 (89 days old). Sources: [release](https://github.com/actions/setup-go/releases/tag/v7.0.0), [release metadata](https://api.github.com/repos/actions/setup-go/releases/tags/v7.0.0), [tag reference](https://api.github.com/repos/actions/setup-go/git/ref/tags/v7.0.0).
- `actions/setup-node@v7.0.0`: `820762786026740c76f36085b0efc47a31fe5020`, released 2026-07-14 (91 days old). Sources: [release](https://github.com/actions/setup-node/releases/tag/v7.0.0), [release metadata](https://api.github.com/repos/actions/setup-node/releases/tags/v7.0.0), [tag reference](https://api.github.com/repos/actions/setup-node/git/ref/tags/v7.0.0).
- These official stable releases exceed the minimum release age and are pinned to full commit SHAs to avoid mutable tags. The selected setup-node [action definition](https://github.com/actions/setup-node/blob/820762786026740c76f36085b0efc47a31fe5020/action.yml) supports `package-manager-cache`; CI explicitly sets it to false and does not request npm caching. Setup-go caching is also disabled because this standard-library-only module has no go.sum.
- Go and Node versions remain sourced from `backend/go.mod` and `.nvmrc`. The test job installs all three independent npm packages with `npm ci`, then calls the root Make targets. It needs no PostgreSQL or Docker services. Workflow permissions are limited to `contents: read`.
- Gitleaks `8.30.1` was released 2026-03-21 (206 days old), meeting the age rule. Sources: [official release](https://github.com/gitleaks/gitleaks/releases/tag/v8.30.1), [release metadata](https://api.github.com/repos/gitleaks/gitleaks/releases/tags/v8.30.1), and [versioned documentation](https://github.com/gitleaks/gitleaks/blob/v8.30.1/README.md). Its documentation and downloaded binary's help confirm `gitleaks git` scans Git history.
- Official assets: [Linux x64 archive](https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz) and [checksums file](https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_checksums.txt). The official Linux x64 SHA256 is `551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb`, matching the supplied candidate. Independent `sha256sum --check --strict` verification passed before extraction; the binary reported `8.30.1`.
- CI downloads both assets to `$RUNNER_TEMP`, compares the official checksum to the pinned value, verifies the archive bytes, and only then extracts and executes the binary. Its separate job checks out full history with `fetch-depth: 0` and runs `gitleaks git --redact --exit-code 1 .`; findings fail the job without allowlisting.
- Local validation used Docker with Go 1.27.1, Node 24.21.0, npm 11.19.0, and GNU Make 4.3. All three clean npm installs, all four individual Make test targets, and combined `make test` passed. Temporary failing tool stubs caused each test target and combined test to exit 2. Missing-.env validation used an empty disposable directory with the real Makefile, exited 2, and never moved or read the private environment file.
- The disposable Git-history detection test committed one clearly fake randomly generated token outside FleetOps; Gitleaks found one leak and exited 1. The exact workflow shell script was also executed against disposable synthetic history: both downloads, checksum verification, and version check passed, and the finding produced exit 1. The initial harness had a Windows line-ending parsing error after detection; rerunning with Unix line endings passed. No application changes were needed.
- Docker lifecycle checks used Windows GNU Make with an explicit Git Bash shell because no user WSL distribution is installed. `make up` passed, PostgreSQL reported healthy on `127.0.0.1:5432`, and `make down` passed. The existing `fleetops_postgres_data` volume retained its name and creation timestamp after shutdown. README setup instructions use WSL as required.
- FleetOps has no HEAD commit. Its final pre-push full-history scan is pending: uncommitted files have not been scanned by a history scan. The owner must create the local commit, scan it with the verified binary, and resolve findings before pushing. No FleetOps commit or push was performed.
- CI: Not verified. The owner must provide actual push and throwaway pull-request run URLs and configure both `test` and `gitleaks` as required branch-protection checks. Setup remains In progress until the owner's separate acceptance commit.
