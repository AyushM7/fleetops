Status: Phase 5 AI evaluation report prepared for engineering review.

# FleetOps

FleetOps is an AI-assisted warehouse fleet-management platform with a deterministic
Go simulator, incident detection, authenticated GraphQL proposal lifecycle and a
React operator review flow. Its evaluation framework separates recommendation
quality, host grounding, Go safety validation and simulated recovery effects.

**LLM proposes. Go validates. Human approves. Go executes.**

Phase 5 evaluated 12 decision scenarios, 20 adversarial contract cases and five
paired Go experiments. A saved OpenAI `gpt-6-luna` run passed 11/12 decision trials;
the poisoned-runtime wrong action was rejected by host validation. The prompt
supplies a deterministic action hint, so this measures assisted contract compliance,
not independent action discovery or general reliability. An isolated Go experiment
delivered blocked J1 at tick 45 while its paired baseline remained unfinished at
242. Real-model evaluation did not submit proposals, exercise human approval,
execute recommendations or test PostgreSQL audits. Exact API spending is unconfirmed.

See the [final Phase 5 report](docs/phase5-evaluation-report.md),
[saved live evidence](docs/evidence/p5-04-live-full.json) and
[reproducible offline commands](evals/README.md). No new API calls were made to
prepare the final report. Phase 4's separate end-to-end evidence is documented in
[its acceptance report](docs/phase4-acceptance.md).

| Component | Status |
| --- | --- |
| Go simulator, scheduler and routing | Implemented; deterministic recovery experiments available |
| Validator and incident detector | Implemented; Go safety/incident regressions available |
| PostgreSQL, authenticated GraphQL and operator UI | Implemented; prior Phase 4 acceptance evidence, not revalidated end-to-end in Phase 5 |
| Mastra agent | Grounded reasoning with host validation and independent approval requirements |
| Evals | Phase 5 report and saved live evidence available for review |
| Public deployment | Outside Phase 5 evaluation scope |

## Repository layout

- `backend/`: Go simulator, incidents, safety, proposal lifecycle, persistence and GraphQL.
- `agent/`: TypeScript/Mastra reasoning, grounding and host submission workflow.
- `web/`: React operator dashboard.
- `evals/`: scenario dataset, offline/adversarial/model evaluation and Go recovery orchestration.
- `docs/`: architecture, frozen safety rules, acceptance evidence and evaluation reports.

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
