"""Lightweight workflow and mocked publisher tests; no Docker/network access."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = yaml.load((ROOT / ".github/workflows/publish-image.yml").read_text(), Loader=yaml.BaseLoader)
BASH = shutil.which("bash") if os.name != "nt" else "C:/Program Files/Git/bin/bash.exe"
DIGEST = "sha256:" + "a" * 64


def bash_path(path: Path) -> str:
    value = path.as_posix()
    return f"/{value[0].lower()}{value[2:]}" if os.name == "nt" else value


class WorkflowTests(unittest.TestCase):
    def test_trusted_main_amd64_and_minimal_permissions(self) -> None:
        self.assertEqual(WORKFLOW["on"], {"push": {"branches": ["main"]}, "workflow_dispatch": ""})
        self.assertEqual(WORKFLOW["permissions"], {})
        job = WORKFLOW["jobs"]["publish"]
        self.assertEqual(job["runs-on"], "ubuntu-24.04")
        self.assertEqual(job["permissions"], {"contents": "read", "packages": "write"})
        self.assertIn("github.repository == 'AyushM7/fleetops'", job["if"])
        self.assertIn("github.ref == 'refs/heads/main'", job["if"])
        self.assertIn("github.event.repository.private == false", job["if"])
        self.assertEqual(WORKFLOW["concurrency"]["cancel-in-progress"], "false")
        self.assertIn("github.sha", WORKFLOW["concurrency"]["group"])
        builder = next(step for step in job["steps"] if step.get("name") == "Create CI BuildKit builder")
        self.assertIn("--driver docker-container --use", builder["run"])
        self.assertIn("docker buildx inspect --bootstrap", builder["run"])

    def test_checkout_credentials_and_login_scope(self) -> None:
        steps = WORKFLOW["jobs"]["publish"]["steps"]
        actions = [step for step in steps if "uses" in step]
        self.assertEqual(len(actions), 1)
        self.assertRegex(actions[0]["uses"], r"^actions/checkout@[0-9a-f]{40}$")
        self.assertEqual(actions[0]["with"]["persist-credentials"], "false")
        login = next(step for step in steps if step.get("name") == "Authenticate to GHCR")
        self.assertEqual(login["env"], {"GHCR_TOKEN": "${{ secrets.GITHUB_TOKEN }}"})
        self.assertIn("--password-stdin", login["run"])
        build = next(step for step in steps if step.get("name") == "Build and publish SHA image")
        self.assertNotIn("env", build)
        self.assertEqual(steps[-1]["if"], "always()")
        self.assertEqual(steps[-1]["run"], "docker logout ghcr.io")

    def test_bash_syntax(self) -> None:
        subprocess.run([BASH, "-n", str(ROOT / "deploy/pub03/publish-image.sh")], check=True)
        for step in WORKFLOW["jobs"]["publish"]["steps"]:
            if "run" in step:
                subprocess.run([BASH, "-n", "-c", step["run"]], check=True)


class PublisherTests(unittest.TestCase):
    def setUp(self) -> None:
        artifact_root = ROOT / "evals/dist/pub03/ghcr-tests"
        artifact_root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="mock-", dir=artifact_root)
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        self.bin = self.work / "bin"
        self.bin.mkdir()
        self.sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        self.env = dict(os.environ, GITHUB_REF="refs/heads/main", GITHUB_SHA=self.sha,
                        GITHUB_REPOSITORY="AyushM7/fleetops", GITHUB_SERVER_URL="https://github.com",
                        RUNNER_TEMP=self.work.as_posix(), GITHUB_STEP_SUMMARY=(self.work / "summary").as_posix(),
                        MOCK_BIN=bash_path(self.bin), MOCK_DIGEST=DIGEST, MOCK_REGISTRY="missing",
                        DOCKER_CALLS=(self.work / "calls").as_posix(),
                        PUBLISH_SCRIPT=(ROOT / "deploy/pub03/publish-image.sh").as_posix())
        self.write_executable("python3", f'#!/usr/bin/env bash\nexec "{Path(sys.executable).as_posix()}" "$@"\n')
        self.write_executable("docker", '''#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >> "$DOCKER_CALLS"
if [[ "$1 $2 $3" == 'buildx imagetools inspect' ]]; then
  case "$MOCK_REGISTRY" in
    exists) printf '{"digest":"%s"}' "$MOCK_DIGEST" ;;
    missing) echo 'manifest unknown: not found' >&2; exit 1 ;;
    outage) echo '503 service unavailable' >&2; exit 1 ;;
    denied) echo 'unauthorized: authentication required' >&2; exit 1 ;;
  esac
elif [[ "$1 $2" == 'buildx build' ]]; then
  printf '{"containerimage.digest":"%s"}' "$MOCK_DIGEST" > "$METADATA"
else
  echo 'Unexpected Docker invocation' >&2; exit 1
fi
''')

    def write_executable(self, name: str, source: str) -> None:
        path = self.bin / name
        path.write_bytes(source.encode())
        path.chmod(0o700)

    def publish(self, **overrides: str) -> subprocess.CompletedProcess:
        return subprocess.run([BASH, "-c", 'export PATH="$MOCK_BIN:$PATH"; bash "$PUBLISH_SCRIPT"'],
                              cwd=ROOT, env=dict(self.env, **overrides), capture_output=True, text=True)

    def calls(self) -> list[str]:
        path = self.work / "calls"
        return path.read_text().splitlines() if path.exists() else []

    def test_new_image_uses_existing_recipe_full_sha_and_digest(self) -> None:
        result = self.publish()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertEqual(len(calls), 2)
        self.assertIn("--platform linux/amd64 --file Dockerfile", calls[1])
        image = f"ghcr.io/ayushm7/fleetops:{self.sha}"
        self.assertIn(f"--tag {image}", calls[1])
        self.assertIn(f"org.opencontainers.image.revision={self.sha}", calls[1])
        self.assertIn("org.opencontainers.image.source=https://github.com/AyushM7/fleetops", calls[1])
        self.assertIn("--push .", calls[1])
        for prohibited in ("--build-arg", "--secret", ":latest", "--ssh"):
            self.assertNotIn(prohibited, calls[1])
        self.assertIn(f"FLEETOPS_IMAGE={image}@{DIGEST}", (self.work / "summary").read_text())

    def test_existing_sha_is_never_rebuilt_or_retagged(self) -> None:
        result = self.publish(MOCK_REGISTRY="exists")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls()), 1)
        self.assertIn("reusing", result.stdout)

    def test_registry_errors_prevent_build_and_push(self) -> None:
        for state in ("outage", "denied"):
            with self.subTest(state=state):
                result = self.publish(MOCK_REGISTRY=state)
                self.assertNotEqual(result.returncode, 0)
        self.assertTrue(all("imagetools inspect" in call for call in self.calls()))

    def test_invalid_branch_sha_or_checkout_prevents_registry_access(self) -> None:
        for override in ({"GITHUB_REF": "refs/heads/feature"}, {"GITHUB_SHA": "bad"}, {"GITHUB_SHA": "b" * 40}):
            with self.subTest(override=override):
                self.assertNotEqual(self.publish(**override).returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_invalid_registry_digest_fails(self) -> None:
        result = self.publish(MOCK_REGISTRY="exists", MOCK_DIGEST="invalid")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("digest missing or invalid", result.stderr)
        self.assertFalse((self.work / "summary").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
