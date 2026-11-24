# FleetOps V2 Lightsail replacement

Use the existing V1 host as the rollback target. The inventory confirmed Ubuntu 24.04 on x86_64, 2 vCPUs, a 40 GiB disk, and 1 GiB of nominal RAM. PostgreSQL, Caddy, and FleetOps were running. An authorized operator must verify the running image digests, actual PostgreSQL version and migration ledger, and mounted volumes before planning a data migration.

## Prepare and back up

1. Record the live Compose project and image digests, PostgreSQL major version, migration versions and checksums, data and evidence mounts, TLS volumes, and static IP attachment. Inspect configuration without printing secrets.
2. After approval, take an instance snapshot and an encrypted, off-host PostgreSQL custom-format dump. Export role definitions without passwords and copy private evidence separately. Check backup hashes and restore into an isolated database. Verify representative data, access, and migrations with the exact V2 release image.
3. Confirm which V1 data needs to carry over and reserve a final write-freeze window if needed. Keep the original image and volumes throughout the rollback window.

## Stage and verify

1. Start with a separate 2 GiB / 60 GiB Linux instance for deterministic V2. The published default image bundles React into Go. The [Compose file](../deploy/pub03/compose.yml) uses PostgreSQL 18.6 and Caddy. Only Caddy publishes ports. Pin image digests and use a private deployment environment based on the [template](../deploy/pub03/.env.example). Set the staging hostname and matching HTTPS origin. Create the private evidence directory for the app UID and leave at least 5 GiB plus 32 MiB free on its filesystem.
2. Validate the staging configuration with `docker compose --env-file <private-env> -f deploy/pub03/compose.yml config --quiet`. Verify HTTPS, `/healthz`, `/readyz`, and the [smoke test](../deploy/pub03/smoke.py). Check isolated visitors, advancing SSE ticks, the default warehouse, durable audit and archive writes, and representative memory, CPU and disk use. Recheck durability after a controlled staging restart.
3. If separately authorized, start Mastra testing on 4 GiB / 80 GiB. Build the Dockerfile's `operations` target separately and pin its digest. The base Compose app has internal-only networks. An approved Operations configuration must give Go bounded outbound HTTPS access to the provider, pass the reviewed settings into the app container, and keep its database network private. Use a dedicated provider key, shared PostgreSQL accounting, explicit caps, restricted operator access, and an independently approved execution. See the [Operations controls](operations-manager.md).

## Cut over and roll back

After approval and successful rehearsal, freeze V1 writes if needed, take the final dump, restore and verify the target, then reattach the existing static IP to V2. Confirm HTTPS, readiness, and durable writes using the production hostname.

If readiness, TLS, isolation, or durable writes fail, stop V2 writes and move the IP back to V1. Keep V2's database and evidence for reconciliation. Preserve the old host, database, TLS state, and image digest until the rollback window has closed.
