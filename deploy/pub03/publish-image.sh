#!/usr/bin/env bash
set -euo pipefail

test "$GITHUB_REF" = refs/heads/main
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]
test "$(git rev-parse HEAD)" = "$GITHUB_SHA"
test "$(uname -m)" = x86_64

export IMAGE="ghcr.io/${GITHUB_REPOSITORY,,}:$GITHUB_SHA"
export METADATA="$RUNNER_TEMP/fleetops-image.json"
inspect_error="$RUNNER_TEMP/fleetops-inspect-error.log"

# Preserve an existing SHA tag on retries; fail closed on registry/auth outages.
if docker buildx imagetools inspect "$IMAGE" --format '{{json .Manifest}}' > "$METADATA" 2> "$inspect_error"; then
  echo 'SHA image already published; reusing its digest.'
elif grep -Eqi 'manifest unknown|name unknown|not found|no such manifest' "$inspect_error"; then
  docker buildx build --platform linux/amd64 --file Dockerfile \
    --tag "$IMAGE" \
    --label "org.opencontainers.image.source=$GITHUB_SERVER_URL/$GITHUB_REPOSITORY" \
    --label "org.opencontainers.image.revision=$GITHUB_SHA" \
    --provenance=mode=min --metadata-file "$METADATA" --push .
else
  cat "$inspect_error" >&2
  exit 1
fi

python3 - <<'PY'
import json
import os
import re
from pathlib import Path

metadata = json.loads(Path(os.environ["METADATA"]).read_text())
digest = metadata.get("containerimage.digest") or metadata.get("digest", "")
if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
    raise SystemExit("Registry image digest missing or invalid")
image = os.environ["IMAGE"]
with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as summary:
    summary.write(f"Published/reused linux/amd64 production image\n\n"
                  f"SHA tag: `{image}`\n\n"
                  f"Digest-pinned Compose setting:\n\n"
                  f"```text\nFLEETOPS_IMAGE={image}@{digest}\n```\n")
print(f"FLEETOPS_IMAGE={image}@{digest}")
PY
