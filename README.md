Status: Phase 1 complete; Phase 2 feature-complete but not fully accepted.

# FleetOps

FleetOps is a planned robot fleet operations system for coordinating jobs, routes, charging, and incident response. Operators need assistance that proposes useful actions while preserving explicit approval, deterministic safety checks, and an audit trail. The intended system pairs LLM proposals with Go validation; this repository currently contains setup scaffolding, a Go health server, TypeScript package checks, a minimal React app, and local PostgreSQL configuration.

LLM proposes, Go decides

| Component | Status |
| --- | --- |
| Setup | In progress |
| Go simulator | Phase 1 complete; Phase 2 feature-complete, not fully accepted |
| Scheduler and routing | Phase 2 feature-complete, not fully accepted |
| Validator | Not started |
| Postgres persistence | Not started |
| GraphQL API | Not started |
| Incident detector | Not started |
| Agent | Not started |
| Evals | Not started |
| Operator UI | Not started |
| Replay mode and deployment | Not started |

## Repository layout

- `backend/`: Go health server and placeholders for future backend components.
- `agent/`: independent TypeScript package and placeholders for tools and workflows.
- `web/`: minimal React/Vite application.
- `evals/`: independent TypeScript package and placeholders for scenarios and results.
- `docs/`: architecture placeholder, frozen safety rules, setup decisions, and evaluation placeholder.

## Local setup

Install the Node.js version pinned in `.nvmrc`, Go from `backend/go.mod`, GNU Make, and Docker with Compose. Windows users need WSL for `make`; run these commands in WSL with those tools available and Docker Desktop integration enabled. Version evidence is recorded in [docs/decisions.md](docs/decisions.md).

Run from the repository root, in this order:

```sh
cp .env.example .env
(cd agent && npm ci)
(cd web && npm ci)
(cd evals && npm ci)
make test
make up
make down
```

After copying `.env.example`, replace the database placeholders privately in `.env` before `make up`. Never commit or display credentials. `make up` fails clearly if `.env` is missing and waits for PostgreSQL health. While it is running, use `docker compose ps` to check health. Use `docker volume ls` before and after `make down` to confirm `fleetops_postgres_data` persists. Never use `down -v`.

PostgreSQL binds only to `127.0.0.1:5432`. Set `POSTGRES_HOST_PORT` in your shell to override the host port if necessary. The named volume mounts at `/var/lib/postgresql` for the PostgreSQL 18 layout.

## Setup checks

Run `git check-ignore .env` (exit 0), `git ls-files -- .env` (empty), and `git check-ignore -q .env.example` (exit 1). Run `git diff --check` before review. `make test` runs backend tests and vet plus tests and strict typechecking for all three npm packages; individual targets are `test-backend`, `test-agent`, `test-web`, and `test-evals`.

CI runs the `test` and `gitleaks` jobs on pushes and pull requests without starting a database. Gitleaks verifies its downloaded binary and scans Git history; uncommitted files are not covered. Before the first public push, the owner must create the local commit and run the verified Gitleaks binary with `gitleaks git --redact --exit-code 1 .` from the repository root, resolving any findings before pushing.

CI acceptance remains unverified until the owner supplies successful push and throwaway pull-request run URLs. The owner must configure both jobs as required branch-protection checks. Setup stays **In progress** until the owner records local and CI acceptance in a separate commit.
