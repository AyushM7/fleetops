# PUB-03 — Lightsail deployment

Current continuation: **CONDITIONAL — GHCR publishing workflow prepared and locally
verified; actual Actions build/publication awaits integration into main.** The user
reports an existing Ubuntu 24.04 / 1 GB Lightsail host, about 549 MiB available RAM,
36 GB disk free, installed Docker/Compose and configured DNS/firewall. No AWS or
credential changes are part of this continuation. Build on GitHub, pull on Lightsail.

The original local preparation record below predates that host and is historical:

Assessment: **CONDITIONAL — local preparation; public deployment was not authorized.**
Public URL: **not deployed**. No AWS resource, budget, DNS record or public listener
has been created. No commit, push or merge. Region requested: **us-east-2 (Ohio)**.
Account ID, student credit coverage/expiry, billing email and DuckDNS hostname
remain unverified/unset. Read-only CLI identity lookup failed because the configured
session expired. A profile named `default` is not proof of the intended account.

## GHCR build and pull continuation

Files for this continuation only: `.github/workflows/publish-image.yml`,
`deploy/pub03/publish-image.sh`, `deploy/pub03/check-publish.py`, `docs/pub-03.md`.
The existing production Dockerfile, application, Compose, AWS resources and
credentials are unchanged. Prior 2 GB sizing advice below is historical; this
continuation targets the user's existing 1 GB host without on-host compilation.

### Publish from GitHub Actions

After the change is reviewed and merged into **main**, the **Publish production
image** workflow automatically runs on the standard GitHub-hosted **ubuntu-24.04
amd64** runner. It can also be manually dispatched from Actions with branch main
selected. It never publishes PR code, other branches, forks or private repositories.
It uses the existing root Dockerfile and explicit `--platform linux/amd64`, with
no QEMU, new project dependencies, external build service or paid model call.
The only third-party action is the existing commit-pinned checkout action.
Docker CLI creates a temporary docker-container BuildKit builder on the Actions
runner for registry export and provenance; no builder runs on Lightsail.

Image naming is `ghcr.io/ayushm7/fleetops:<full-40-character-commit-SHA>`. There are
no latest/main tags. Same-commit runs are serialized, and an existing SHA image
is reused instead of overwritten. Auth/server errors abort rather than authorize
a rebuild. Missing manifests authorize the first build/push. An existing image
must remain available in GHCR for retry and rollback; don't delete it as cleanup.
Registry tags can still be changed by an authorized external publisher: **use the
SHA tag plus digest printed in the workflow summary for strict immutability**.

Top-level token permissions are empty; the publishing job gets only
`contents: read` and `packages: write`. Checkout does not persist credentials.
Only the login step receives the ephemeral `GITHUB_TOKEN`, through password-stdin;
Docker auth lives in a runner-temp config, and logout runs even after failure.
There is no AWS access, SSH deployment, PAT, registry credential on Lightsail,
OIDC, attestation-write or repository-write permission. BuildKit's minimal
provenance records build metadata without requiring GitHub artifact attestations.
No env files, private runtime values, secret build arguments or SSH mounts enter
the build; the unchanged `.dockerignore` restricts the production context.

Check the repository's existing **CI** workflow as well as **Publish production
image** before deployment. Publication does not imply application test acceptance,
and nothing in this workflow automatically deploys the image.

### One-time public package setting

After the first successful publish, open
[FleetOps container package](https://github.com/users/AyushM7/packages/container/package/fleetops),
then **Package settings → Danger Zone → Change visibility → Public**. Confirm the
package is linked to `AyushM7/fleetops`; the source label and publishing token link
it automatically. If a pre-existing package is unlinked, grant this repository
Actions access in that package's settings before retrying. Never broaden the
workflow token to compensate for a package-access problem.

Public source repository visibility alone does **not** make a new GHCR package
public. A newly published package defaults to private; public packages support
anonymous pulls. This one-time package setting is performed by its owner, not by
the workflow. [GitHub Container Registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
No package visibility has been changed by this local task.

### Exact Lightsail commands (operator-run; no local server build)

In the **existing server clone**, update main normally, then use the SHA and digest
from the successful publishing run. Don't substitute the unmerged topic-branch SHA.
The private production env file must already contain the host/email, database
passwords, reviewed Caddy image and evidence path from the original setup. Do not
overwrite it with the example or print expanded Compose config.

```bash
# Run from the existing fleetops clone on Lightsail.
git switch main
git pull --ff-only
RELEASE_SHA=$(git rev-parse HEAD)
# Confirm the successful Actions run built exactly RELEASE_SHA.
EXPECTED_DIGEST='sha256:PASTE_64_HEX_DIGEST_FROM_WORKFLOW_SUMMARY'
export FLEETOPS_IMAGE="ghcr.io/ayushm7/fleetops:${RELEASE_SHA}@${EXPECTED_DIGEST}"

dc() {
  sudo env FLEETOPS_IMAGE="$FLEETOPS_IMAGE" docker compose \
    --env-file /srv/fleetops/production.env \
    -f deploy/pub03/compose.yml "$@"
}
dc config --quiet

# Empty temporary Docker config proves this image is anonymously readable.
# This does not alter existing Docker credentials or perform docker login.
ANON_CONFIG=$(mktemp -d)
sudo docker --config "$ANON_CONFIG" pull "$FLEETOPS_IMAGE"

# Pull through the actual existing Compose service, never build on Lightsail.
dc pull app
sudo docker image inspect "$FLEETOPS_IMAGE" --format '{{.Os}}/{{.Architecture}} {{.Config.User}}'
# Expected: linux/amd64 65532:65532
```

To persist the choice, use `sudoedit /srv/fleetops/production.env` and replace only
its `FLEETOPS_IMAGE=` line with the exact line printed in the workflow summary.
Keep all existing secrets and other settings unchanged. The function above also
passes that value explicitly so sudo does not discard the shell override.

For a fresh stack, once private evidence-directory ownership and DB env are ready,
the operator can run `dc up -d --no-build --wait --wait-timeout 180`. For an already
running stack, first take the graceful backup described below, then run
`dc up -d --no-deps --no-build --wait --wait-timeout 180 app`. These are deployment
commands for the operator; **this task does not execute them or open any endpoint**.
Verify `dc ps`, HTTPS `/healthz`, `/readyz`, the existing `smoke.py` and observed
memory. Retain all database/evidence/Caddy volumes. Pulling alone starts no service.

Rollback uses the previous successful `FLEETOPS_IMAGE=...:<SHA>@sha256:<digest>`
with the same Compose file/env/volumes, after graceful shutdown and backup. It
requires no rebuild, registry login, database reset or AWS change. Don't run
`down -v`, prune images needed for rollback, or print runtime secrets.

### Continuation verification

`python deploy/pub03/check-publish.py`: **8 tests PASS**, final run **1.968s**.
Checks workflow triggers, public upstream/main restriction, amd64 runner, pinned
checkout, token scope/cleanup, Bash syntax and actual Bash publisher execution
against a mocked Docker executable: new publication, reuse without retagging,
registry/auth failures, invalid SHA/branch/checkout and invalid digest. No real
Docker build, registry request, model inference or AWS operation is performed by
these tests. Existing PyYAML and Git Bash were reused; no dependencies installed.
The first sandboxed run failed because Windows blocked Git Bash shared-memory
initialization; the same tests passed outside that sandbox without code relaxation.
`python deploy/pub03/check-config.py`: **5 tests PASS**, **0.001s**, preserving
existing Compose/proxy/database invariants. `git diff --check` exited **0**; only
the repository's ordinary Windows LF/CRLF conversion warning was emitted.

Operator-run Git integration from the current reviewed working tree:

```bash
git switch -c codex/pub03-ghcr
git add .github/workflows/publish-image.yml deploy/pub03/publish-image.sh deploy/pub03/check-publish.py docs/pub-03.md
git diff --cached --check
git diff --cached --stat
git commit -m "PUB-03: publish production image to GHCR"
git push -u origin codex/pub03-ghcr
```

Open a PR into main, review the four-file diff and existing CI results, then merge
it through the repository's normal process. The main push publishes the merge's
full SHA, which is the revision to deploy. No Git mutation above was executed here;
the unrelated untracked handoff is deliberately absent from `git add`.

Actual GitHub Actions build, GHCR publication, public package visibility and
anonymous Lightsail pull remain **unexecuted** because no commit or push was
authorized. A real image digest is available only after the workflow succeeds;
none is fabricated in this document. Historical PUB-03 image acceptance below
is not claimed as a new build or cloud verification.

## Architecture and sizing (original local preparation)

One Linux amd64 Lightsail instance runs Caddy, the scratch Go image containing the
React dashboard, and PostgreSQL 18.6. Production never starts Node/Mastra or an LLM.
Docker/Compose and Caddy are the explicitly requested infrastructure; no project
dependency was added. The Go health helper uses only the standard library.

Proposed starting bundle: **2 vCPU / 2 GiB / 60 GB SSD / public IPv4, $12/month**.
This remains subject to an actual host smoke. The eight-visitor local sample was
Go **8.773 MiB**, PostgreSQL **86.84 MiB**, Caddy **13.83 MiB** (about **109.4 MiB**
total Docker working-set reporting). This is one sample after preset operations,
not peak RSS, a twenty-minute soak, or a Lightsail CPU-credit benchmark. The
2 GiB recommendation reserves room for archive bursts, the host and builds; the
smaller 1 GiB host has not been certified by this run.
The 1 GiB bundle leaves insufficient reserve against the configured container
ceilings: Go 512 MiB, PostgreSQL 384 MiB, Caddy 128 MiB, plus Linux/Docker. Go has
GOMEMLIMIT=384MiB and two execution threads. PostgreSQL allows 32 connections
(the app uses at most 18), 64 MiB shared buffers and 2 MiB per work_mem operation.
These limits are guardrails, not a claim that eight sustained users fit in 1 GiB.
The original source-build approach is superseded by the GHCR continuation above:
build only on GitHub Actions and pull the result onto the existing 1 GB host.

[AWS bundle pricing](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-bundles.html)
lists the $12 public IPv4 bundle. An
[attached static IP is free](https://docs.aws.amazon.com/lightsail/latest/userguide/understanding-static-ip-addresses-in-amazon-lightsail.html).
[Snapshots](https://aws.amazon.com/lightsail/pricing/) are $0.05/GB-month: allow
roughly $3 for one full 60 GB equivalent, with actual incremental storage varying.
Plan **$12 base, about $15/month with that snapshot allowance**, before tax,
transfer overages, additional retained snapshots or detached-IP charges. DuckDNS
and Caddy certificates require no purchased domain. Do not assume free-tier or
student credits: inspect **Billing and Cost Management → Credits** for eligible
services, remaining balance, account and expiry before seeking final approval.
Select an account-paid plan if required by Lightsail; don't change account plans
without approval. Verify the regional bundle API price before creation.

## Network, credentials and persistence

Only Caddy publishes TCP 80 (redirect/ACME) and 443 (HTTPS). There is no public
backend, database, Caddy administration or Mastra port. The database network and
proxy-to-app network are internal Docker bridges. Caddy alone joins the egress
network. Check that 172.30.83.0/29 does not overlap host/VPC/Docker networks.
Changing it requires updating Compose's proxy addresses and exact trusted /32.

Caddy replaces X-Forwarded-For/Host/Proto and removes Forwarded/X-Real-IP. It
preserves Host and performs no mutation retry. The app trusts only 172.30.83.2/32
and 127.0.0.1/32; loopback is for `/healthcheck` inside its container. It probes
liveness, not capacity-sensitive readiness, so eight visitors won't prevent
startup or cause a healthy process to be restarted. Probe `/readyz` separately.
The Caddy admin probe is container loopback only. Docker health status does not
itself restart a running unhealthy service; inspect and reconcile the cause.

The app runs as 65532 with read-only root, no capabilities, bounded tmpfs, and a
private evidence bind mounted from `/srv/fleetops/evidence`. PostgreSQL data uses
the PG18 `/var/lib/postgresql` volume layout. Caddy ACME state has separate durable
volumes. Never run `down -v`, volume prune, system prune or remove evidence to free
space. Stopping/recreating an application container must retain all these mounts.

The private `.env` contains independently generated bootstrap and app passwords.
Use hex to avoid URL/interpolation ambiguity. The bootstrap postgres role stays
private; `fleetops_owner` is LOGIN, NOSUPERUSER, NOCREATEDB, NOCREATEROLE and
NOREPLICATION, owning only the dedicated fleetops database. Init SQL runs only on
a fresh volume. Changing `.env` later does not rotate existing database passwords.
Database traffic uses SCRAM authentication over a private, single-host internal
bridge (sslmode=disable). It is not encrypted on that bridge. No remote database
or transit TLS is claimed; changing that topology requires verified DB TLS.

Production retains PUB-02's Secure/HttpOnly/SameSite=Strict/host-only cookies,
twenty-minute lifetime, eight-owner admission cap, human approval and Go safety
gates. App restart invalidates all visitor sessions and archives them on graceful
shutdown. It doesn't restore a live simulator from a cookie. Crash-retained schemas
block admission until manual reconciliation. Evidence admission stops at 512 MiB,
10,000 entries, or less than five GiB free plus headroom; preserve and export it,
don't delete it to bypass admission. Maintain at least 10 GiB host free operationally.
External-link navigation can be denied by existing strict Fetch-Site policy; open
the demo via direct address navigation. That policy was not changed in this ticket.

Logs use Docker's local driver capped at 3 × 10 MB per service. Caddy access/debug
logs are disabled so request headers, cookie capabilities and query strings aren't
logged. PostgreSQL statement logging is disabled, including error-statement text.
App events remain sanitized. Root/Docker administrators can inspect private env;
do not expose Docker's socket or grant Docker access to untrusted users.

## Approval gate and AWS setup

All commands below are a runbook, **not evidence of execution**. Stop before AWS
mutations or opening a public endpoint until the user approves the exact account,
region, bundle/cost, static IP, snapshot policy, billing alerts and hostname.
Never paste credentials, private keys, DuckDNS tokens or session cookies into chat.

Read-only preflight with the explicitly selected profile:

```bash
export AWS_PROFILE=USER_SELECTED_PROFILE AWS_REGION=us-east-2 AWS_PAGER=''
aws sts get-caller-identity
aws lightsail get-regions --include-availability-zones
aws lightsail get-bundles --include-inactive false
aws lightsail get-blueprints --include-inactive false
```

Confirm Linux amd64 Ubuntu 24.04 LTS availability in the returned Ohio zone;
choose the active 2 GiB IPv4 bundle and record the exact bundle/blueprint IDs and
price. Verify credit terms in the authenticated Billing console. Obtain preferred
email and an available free DuckDNS subdomain in the user's DuckDNS account.
Then request the single explicit provisioning/public-exposure approval.

After approval, configure a **$20/month cost budget**, excluding credits/refunds
so alerts track gross spend: email alerts at 50%, 80%, 100% actual and 100%
forecast. Also enable AWS Free Tier/credit usage alerts in Billing preferences
where available. Use **Billing → Budgets → Create budget → Customize → Cost
budget → Monthly → Fixed $20**, all services in this account, then the thresholds
above and the supplied email. Verify the budget exists and the recipient is
correct. Budget notifications are delayed alerts, not a spending cap.
[AWS Budgets guidance](https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-managing-costs.html).
No billing notification settings have been changed locally.

Use a dedicated SSH public key (private key remains local), then:

```bash
aws lightsail import-key-pair --key-pair-name fleetops-pub03 --public-key-base64 fileb://fleetops-pub03.pub
aws lightsail create-instances --instance-names fleetops-pub03 \
  --availability-zone APPROVED_OHIO_ZONE --blueprint-id VERIFIED_UBUNTU_ID \
  --bundle-id VERIFIED_2GB_IPV4_BUNDLE --key-pair-name fleetops-pub03
aws lightsail allocate-static-ip --static-ip-name fleetops-pub03
aws lightsail attach-static-ip --static-ip-name fleetops-pub03 --instance-name fleetops-pub03
```

Initially replace the instance firewall with SSH TCP 22 restricted to the owner's
current /32 only, using `put-instance-public-ports`; no `0.0.0.0/0` SSH or IPv6
SSH. Use a JSON `--port-infos file://...` list, with
`{"fromPort":22,"toPort":22,"protocol":"tcp","cidrs":["OWNER_IP/32"]}`.
Before release, add TCP 80 and 443 with IPv4 `0.0.0.0/0` to that same list. Leave
IPv6 ingress empty; don't publish an AAAA record. Verify using
`aws lightsail get-instance-port-states --instance-name fleetops-pub03`.
If Docker publishes a port, UFW alone is insufficient: enforce the Lightsail
firewall and verify the actual ports from outside after approved release.

Point the approved DuckDNS A record at the static IP using its authenticated
dashboard (no token in shell history). Clear a stale AAAA record if present.
Verify public DNS resolution before Caddy ACME starts. No recurring DNS updater
is necessary for a static IP. [DuckDNS API reference](https://www.duckdns.org/spec.jsp).

## Linux host and deployment commands

Connect as ubuntu using the approved key and verified host key. Install Docker
Engine and Compose from the
[official Ubuntu repository instructions](https://docs.docker.com/engine/install/ubuntu/).
Use root/sudo for Docker, not an unrestricted public daemon. Enable Docker on boot
and verify `sudo docker version` / `sudo docker compose version`.

Copy the reviewed source to `/srv/fleetops/releases/RELEASE_ID`, excluding `.git`,
all `.env`/credentials, evals/dist, node_modules, build outputs and private evidence.
Use an explicit source manifest; do not upload the entire laptop workspace.
The following shell uses the uploaded release root:

```bash
sudo install -d -m 0700 -o 65532 -g 65532 /srv/fleetops/evidence
sudo install -d -m 0700 /srv/fleetops/backups
cd /srv/fleetops/releases/RELEASE_ID
sudo cp deploy/pub03/.env.example /srv/fleetops/production.env
sudo chmod 600 /srv/fleetops/production.env
# Privately edit production.env: approved hostname/email, immutable release tag,
# CADDY_IMAGE=caddy:2.11.7-alpine@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f
# and two distinct passwords generated by openssl rand -hex 32.
# Set FLEETOPS_IMAGE to the GHCR SHA@digest from the workflow summary.
# Set the existing private evidence directory; preserve all existing secrets.
df -h /srv/fleetops
dc() { sudo docker compose --env-file /srv/fleetops/production.env -f deploy/pub03/compose.yml "$@"; }
dc config --quiet                       # Never print expanded config containing secrets.
dc pull app postgres caddy
dc run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile
dc up -d --no-build --wait --wait-timeout 180
dc ps
curl --fail --silent --show-error https://APPROVED_HOST/healthz
curl --fail --silent --show-error https://APPROVED_HOST/readyz
python3 deploy/pub03/smoke.py https://APPROVED_HOST
dc logs --tail 100                      # Don't enable access/debug/SQL logging.
```

Do not rerun the `cp` over an existing production.env on upgrades. Generate the
initial production.env once, preserve it privately, and update only release image
on upgrades. Keep exact image IDs, source manifest hashes, Compose/proxy config
hashes and command outputs in private deployment evidence. The default Compose
name stays fleetops-pub03 across release directories so volumes are reused.
Observe memory, `docker stats`, disk, readiness and two separate browser profiles.
Run all four presets and inspect pending approval/Go receipts. Prove PostgreSQL
and evidence persistence across a graceful app restart; record actual URL only
after certificate validation and external ingress tests. Confirm 5432, 8080 and
2019 are closed externally (and no IPv6 listener bypass). Don't consider an HTTPS
200 alone to be completed acceptance.

## Backup, restart and rollback

Before upgrades: gracefully `dc stop app`, inspect exit code/logs, and confirm
all visitor schemas have been archived (no `fleetops_visitor_` namespaces). Preserve
archives and database together. With the app stopped:

```bash
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
dc exec -T postgres pg_dump -U postgres -d fleetops -Fc > /srv/fleetops/backups/$STAMP.dump
sudo tar --numeric-owner -C /srv/fleetops -czf /srv/fleetops/backups/$STAMP.evidence.tgz evidence
dc exec -T postgres pg_restore --list < /srv/fleetops/backups/$STAMP.dump > /srv/fleetops/backups/$STAMP.inventory.txt
sha256sum /srv/fleetops/backups/$STAMP.*
```

Run that backup block from a privileged private shell (backup directory is 0700).
Copy verified backups off-host into user-controlled storage; local volumes alone
do not survive instance deletion. For a consistent Lightsail snapshot, stop the
app gracefully and stop PostgreSQL before creating it, wait for snapshot completion,
then start PostgreSQL and the app. Only create snapshots within the approved cost
policy. Never delete an old snapshot/archive automatically.

Rollback application/config: stop app gracefully; preserve current evidence and
database backup; use the previous release directory and previous immutable image
in the same private env, then `dc up -d --no-deps --wait app` and recheck HTTPS.
If proxy configuration changed, validate the previous release's Caddyfile with
`dc run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile`, then
`dc up -d --no-deps --wait caddy` to rebind its mount to that release directory.
Don't change PostgreSQL major version or restore over production for an app rollback.
If migrations are incompatible, restore the dump and evidence into an entirely
separate private test database/volume first, validate with the old release, then
plan an approved recovery. PostgreSQL roles must exist before `pg_restore`; use
the same dedicated role bootstrap. Don't drop schemas to make readiness green.

Crash recovery: stop app, inventory retained schema names/OIDs/owner and existing
archives, take a full DB backup plus evidence copy and verify restore into a new
private volume. The application requires a verified archive with matching owner,
namespace identity and complete rows before schema removal. PUB-02 supplies no
standalone administrative reconciler; obtain a separately reviewed reconciliation
procedure for retained schemas. Preserve schemas, serve maintenance/unavailable
and keep the endpoint closed if reconciliation cannot be established. Merely
restarting repeatedly or deleting orphan schemas is not recovery.

## Verification results

Local evidence root: `evals/dist/pub03/local-20261019/` (ignored, private).
No result from PUB-02 is represented as a PUB-03 rerun.

| Check | Actual result |
| --- | --- |
| `python deploy/pub03/check-config.py` | **5 tests PASS**, final output `Ran 5 tests in 0.002s`, `OK`. Verifies private ports/networks, exact proxy peer, persistence, limits, quoted tmpfs options and image/role recipe. |
| Production Compose `config --quiet` | **Exit 0** using private test env; no resources created by this validation. |
| Caddy 2.11.7 config validation | **Exit 0**, `Valid configuration`; explicit forwarding overrides emit benign redundancy warnings. Official image digest recorded in deployment commands. |
| `source-build-final` | **Exit 0**: Linux `go test -p 1 ./cmd/healthcheck -count=1 -v` passed **2 top-level tests / 4 status subtests**, package **0.019s**. Both untagged static binaries built from current source using cached modules and executable RAM-backed temp/cache. |
| `runtime-image-final` | **Exit 0**: built the scratch runtime from those binaries, production assets and CA roots, using the same final-stage file paths, uid and defaults. |
| `frontend-production-build-node` | **Exit 0**: `tsc --noEmit && vite build`; 29 modules transformed, Vite **824 ms**. All three rebuilt frontend files are SHA-256 identical to those packaged in the tested image. |
| `https-smoke-hostport` | **3 tests PASS**, **111.491s**, exit 0: strict cookies, exact HTTPS/proxy boundary, two visitors, foreign approval denial, all four presets, human approval before progression, Go execution, unchanged second visitor, eight slots with readiness 503/liveness 200 at capacity. |
| `https-smoke-final` | Final runtime image: **3 tests PASS / 1 explicit skip**, **88.525s**, exit 0. Rechecks real JS/CSS delivery, secure cookies, hostile-origin rejection, proxy overwrite, isolation/presets/approval and absence of both current session capabilities and both DB passwords from all three service logs. Capacity was deliberately not repeated after its earlier pass. |
| `persistence` | **Exit 0**, graceful app exit **0**, no OOM, **8** complete final DB archives, **10 proposal rows / 15 audit rows**, **50 evidence files byte-identical** across restart, **0** retained visitor schemas. A dedicated sentinel row survived actual PostgreSQL restart and `pg_dump -Fc`/`pg_restore` into a separate private database. |
| `finalization` | **Exit 0**, all three test services stopped; app exit **0**, **0** retained visitor schemas. Final export has **10** complete DB archives / **76** evidence files, SHA-256 manifest and final DB dump. Original volumes, previous PUB-02 resources and unrelated container remain retained. Final recorded host free space **6,065,266,688 bytes (5.65 GiB)**. |
| Format/syntax | `gofmt -l` produced no paths, PowerShell guard parsed, `git diff --check` exit 0. |

Go server SHA-256:
`c0efe504810dc0c1d35f113c9f250fea6d0103d6dc94f18e870c2d11dc8d7199`.
Health helper SHA-256:
`81fe71356fef70b04847e753ff2d285ff7fc406ff400de7f6ee24688370c6343`.
Final local runtime image identity:
`sha256:3b9a63087fa88ea1795a41d8ececeb590461711f34a03e1c3c698324f1c7872e`.

The full root multi-stage Dockerfile was **not built end to end** on the laptop:
only about 5.7 GiB was free initially. Source compilation used Docker tmpfs caches,
the existing Node dependencies supplied the frontend build, and a small artifact
assembly Dockerfile verified the final runtime image. `npm ci` in the build stage,
registry retrieval on Lightsail and end-to-end target-host image construction
remain deployment checks. No package/image pruning or old-evidence cleanup was used.
Guarded operations stopped at a 5.25 GiB threshold; recorded minima remained above
five GiB. Local HTTPS was bound **only to 127.0.0.1:18443**, with Caddy's local CA
explicitly trusted by the test client, no `--insecure`. No public ACME issuance.

Historical failures retained in local logs: initial Linux test execution was
denied by tmpfs's default noexec; corrected to exec for the build runner only.
The next build completed but its checksum line failed on a CRLF argument; the LF
script rerun exited 0 with both hashes. Initial Caddy creation caught unquoted
comma-separated tmpfs syntax, now quoted and asserted. Initial HTTPS bootstrap
returned 403 because forwarded Host omitted the local port; corrected to
`{http.request.hostport}`, then full HTTPS checks passed. The first frontend
build attempt via the Windows npm.cmd wrapper failed to locate npm; invoking
the installed npm CLI through Node passed without installation or source changes.

No new full Go race suite or frontend/agent unit suite was run: application logic
was unchanged, and real production-image/HTTPS/persistence checks cover this ticket.
Prior PUB-02 race, outage and archive-fault results remain historical. Forced
kill/power-loss recovery and automatic retained-schema reconciliation are not claimed.

Changed files (PUB-03 only):

- `Dockerfile`
- `backend/cmd/healthcheck/main.go`
- `backend/cmd/healthcheck/main_test.go`
- `deploy/pub03/.env.example`
- `deploy/pub03/Caddyfile`
- `deploy/pub03/check-config.py`
- `deploy/pub03/compose.yml`
- `deploy/pub03/guard.ps1`
- `deploy/pub03/init.sql`
- `deploy/pub03/smoke.py`
- `docs/pub-03.md`

The pre-existing untracked handoff is untouched. Frozen schema, safety rules and
tool signatures are unchanged. No secrets or generated build/evidence files belong
to the changed-source list. No requirement about the AWS account/hostname was guessed.

Current remaining gate: integrate the GHCR workflow into main, obtain a successful
Actions run/digest, make the package publicly readable, and perform the operator-run
pull/deployment checks above on the already provisioned host. No AWS provisioning
is requested or performed by this continuation. Public application acceptance,
off-host backup restore and billing-alert delivery have not been independently
verified here.
