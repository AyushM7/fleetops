"""Deployment invariants; uses the existing workstation PyYAML installation."""
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parent
CONFIG = yaml.safe_load((ROOT / "compose.yml").read_text())


class DeploymentTests(unittest.TestCase):
    def test_only_proxy_is_published(self) -> None:
        services = CONFIG["services"]
        self.assertEqual(set(services), {"app", "postgres", "caddy"})
        self.assertNotIn("ports", services["app"])
        self.assertNotIn("ports", services["postgres"])
        self.assertEqual(services["caddy"]["ports"], ["80:80", "443:443"])

    def test_private_networks_and_exact_proxy(self) -> None:
        self.assertTrue(CONFIG["networks"]["database"]["internal"])
        self.assertTrue(CONFIG["networks"]["proxy"]["internal"])
        services = CONFIG["services"]
        self.assertEqual(services["postgres"]["networks"], ["database"])
        self.assertEqual(services["caddy"]["networks"]["proxy"]["ipv4_address"], "172.30.83.2")
        self.assertEqual(services["app"]["environment"]["FLEETOPS_TRUSTED_PROXIES"], "172.30.83.2/32,127.0.0.1/32")

    def test_runtime_limits_and_durability(self) -> None:
        for service in CONFIG["services"].values():
            self.assertEqual(service["restart"], "unless-stopped")
            for key in ("healthcheck", "mem_limit", "cpus", "pids_limit", "logging"):
                self.assertIn(key, service)
        app = CONFIG["services"]["app"]
        self.assertTrue(app["read_only"])
        self.assertEqual(app["user"], "65532:65532")
        self.assertFalse(app["volumes"][0]["bind"]["create_host_path"])
        self.assertEqual(app["healthcheck"]["test"], ["CMD", "/healthcheck"])
        for service in ("app", "caddy"):
            self.assertEqual(CONFIG["services"][service]["tmpfs"], ["/tmp:size=16m,mode=1777"])
        self.assertIn("postgres_data:/var/lib/postgresql", CONFIG["services"]["postgres"]["volumes"])

    def test_live_recovery_overlay_is_app_only(self) -> None:
        overlay = yaml.safe_load((ROOT / "compose.operations.yml").read_text())
        self.assertEqual(set(overlay["services"]), {"app"})
        app = overlay["services"]["app"]
        self.assertEqual(set(app), {"environment", "networks"})
        self.assertEqual(app["networks"], {"operations-egress": {}})
        self.assertEqual(overlay["networks"], {"operations-egress": {}})
        env = app["environment"]
        self.assertEqual(env["FLEETOPS_OPERATIONS_ENABLED"], "1")
        self.assertEqual(env["FLEETOPS_OPERATIONS_PROTOCOL"], "v3")
        self.assertTrue(env["OPENAI_API_KEY"].startswith("${OPENAI_API_KEY:?"))
        self.assertFalse(any("DAILY" in key for key in env))

    def test_forwarding_is_overwritten(self) -> None:
        caddy = (ROOT / "Caddyfile").read_text()
        for directive in ("header_up -Forwarded", "header_up -X-Real-IP", "header_up X-Forwarded-Proto https", "header_up X-Forwarded-Host {http.request.hostport}", "header_up X-Forwarded-For {http.request.remote.host}"):
            self.assertIn(directive, caddy)
        self.assertNotIn("\n\tlog", caddy)
        self.assertNotIn("lb_retries", caddy)

    def test_role_and_image_recipe(self) -> None:
        sql = (ROOT / "init.sql").read_text()
        self.assertIn("NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION", sql)
        self.assertIn("REVOKE ALL ON DATABASE fleetops FROM PUBLIC", sql)
        dockerfile = (ROOT.parent.parent / "Dockerfile").read_text()
        self.assertIn("./cmd/healthcheck", dockerfile)
        build_stages, public_stage = dockerfile.split("\nFROM scratch\n", 1)
        self.assertIn("COPY --from=backend /healthcheck /healthcheck", public_stage)
        self.assertNotIn("agent/", public_stage)
        self.assertIn("AS operations\n", build_stages)
        self.assertIn("FLEETOPS_OPERATIONS_AGENT=/agent/dist/operations-worker.js", build_stages)


if __name__ == "__main__":
    unittest.main(verbosity=2)
