"""Run against an approved URL, or loopback HTTPS with --ca; never prints cookies."""
import argparse
import http.cookiejar
import json
import re
import ssl
import subprocess
import time
import unittest
import urllib.error
import urllib.request

PARSER = argparse.ArgumentParser()
PARSER.add_argument("origin")
PARSER.add_argument("--ca")
PARSER.add_argument("--capacity", action="store_true", help="Occupies all eight slots until shutdown/expiry")
PARSER.add_argument("--local-project", help="Audit logs of this local Compose project only")
PARSER.add_argument("--private-env", help="Private local env file whose password values must not appear in logs")
ARGS = PARSER.parse_args()
CONTEXT = ssl.create_default_context(cafile=ARGS.ca)


class Visitor:
    capabilities: list[str] = []

    def __init__(self) -> None:
        self.jar = http.cookiejar.CookieJar()
        self.client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=CONTEXT), urllib.request.HTTPCookieProcessor(self.jar))
        code, body, _ = self.request("/")
        assert code == 200 and b"<title>FleetOps</title>" in body
        cookies = list(self.jar)
        assert len(cookies) == 1
        cookie = cookies[0]
        self.capabilities.append(cookie.value)
        assert cookie.name.startswith("__Host-") and cookie.secure and cookie.path == "/"
        assert "HttpOnly" in cookie._rest and cookie._rest.get("SameSite") == "Strict"
        assert not cookie.domain_specified
        assert cookie.value.encode() not in body

    def request(self, path: str, body: dict | None = None, generation: str = "", tick: str = "", headers: dict | None = None) -> tuple[int, bytes, object]:
        time.sleep(0.08)
        fields = {"Origin": ARGS.origin, "Content-Type": "application/json", "X-FleetOps-Demo": "1", "X-FleetOps-Generation": generation, "X-FleetOps-Tick": tick}
        fields.update(headers or {})
        request = urllib.request.Request(ARGS.origin + path, data=None if body is None else json.dumps(body).encode(), headers=fields)
        try:
            response = self.client.open(request, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            assert all(cookie.value.encode() not in raw for cookie in self.jar)
            return response.code, raw, response.headers

    def status(self) -> dict:
        code, raw, _ = self.request("/demo/status", {})
        assert code == 200
        return json.loads(raw)


class HTTPSDeploymentTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.a, cls.b = Visitor(), Visitor()

    def test_health_assets_and_proxy_sanitization(self) -> None:
        for path in ("/healthz", "/readyz"):
            self.assertEqual(self.a.request(path)[0], 200)
        code, html, headers = self.a.request("/")
        self.assertEqual(code, 200)
        self.assertIn("max-age", headers["Strict-Transport-Security"])
        self.assertEqual(headers["Cache-Control"], "no-store")
        assets = re.findall(rb'(?:src|href)="(/assets/[^\"]+)"', html)
        self.assertGreaterEqual(len(assets), 2)
        for asset in assets:
            code, content, _ = self.a.request(asset.decode())
            self.assertEqual(code, 200)
            self.assertGreater(len(content), 100)
        self.assertEqual(self.a.request("/demo/status", {}, headers={"Forwarded": "for=attacker", "X-Real-IP": "bad", "X-Forwarded-Proto": "http", "X-Forwarded-For": "bad, chain", "X-Forwarded-Host": "attacker.invalid"})[0], 200)
        self.assertEqual(self.a.request("/demo/status", {}, headers={"Origin": "https://attacker.invalid"})[0], 403)

    def test_isolation_four_presets_and_human_gate(self) -> None:
        baseline = self.b.status()
        first = self.a.status()
        self.assertNotEqual(first["generation"], baseline["generation"])
        self.assertNotEqual(first["evidence"]["runtimeId"], baseline["evidence"]["runtimeId"])
        foreign = {"query": "mutation($id:ID!){approveProposal(id:$id){execution error}}", "variables": {"id": first["evidence"]["proposalId"]}}
        _, raw, _ = self.b.request("/graphql", foreign, baseline["generation"])
        self.assertNotIn(b'"execution":"succeeded"', raw)
        self.assertEqual(self.a.status()["state"], "awaiting_decision")
        for scenario, tick, actionable in (("routine-charging", 1000, True), ("healthy-operations", 0, False), ("blocked-no-candidate", 42, False), ("blocked-reassignment", 42, True)):
            with self.subTest(preset=scenario):
                previous = self.a.status()
                self.assertEqual(self.a.request("/demo/scenario/" + scenario, {}, previous["generation"])[0], 200)
                state = self.a.status()
                self.assertEqual(state["scenarioId"], scenario)
                self.assertEqual(state["snapshot"]["tick"], tick)
                self.assertEqual(state["evidence"]["proposalSource"], "go-scripted" if actionable else "none")
                if actionable:
                    self.assertEqual(self.a.request("/demo/step", {}, state["generation"], str(tick))[0], 409)
                    decision = {"query": "mutation($id:ID!){approveProposal(id:$id){state execution executionAttempted error}}", "variables": {"id": state["evidence"]["proposalId"]}}
                    code, raw, _ = self.a.request("/graphql", decision, state["generation"])
                    self.assertEqual(code, 200)
                    self.assertEqual(json.loads(raw)["data"]["approveProposal"]["execution"], "succeeded")
                self.assertEqual(self.a.request("/demo/step", {}, state["generation"], str(tick))[0], 200)
                after = self.a.status()
                self.assertEqual(after["snapshot"]["tick"], tick + 1)
                if scenario == "routine-charging":
                    self.assertEqual(after["snapshot"]["robots"][0]["battery"], 52)
                if scenario == "blocked-reassignment":
                    self.assertEqual(after["snapshot"]["jobs"][0]["assignedRobot"], "R3")
                self.assertEqual(self.b.status(), baseline)

    @unittest.skipUnless(ARGS.capacity, "capacity run not requested")
    def test_z_capacity(self) -> None:
        self.extra = [Visitor() for _ in range(6)]
        self.assertEqual(self.a.request("/readyz")[0], 503)
        self.assertEqual(self.a.request("/healthz")[0], 200)
        self.assertTrue(self.b.status()["ready"])

    @unittest.skipUnless(ARGS.local_project, "local log audit not requested")
    def test_zz_private_logs(self) -> None:
        secrets = list(Visitor.capabilities)
        if ARGS.private_env:
            with open(ARGS.private_env, encoding="utf-8") as file:
                secrets.extend(line.strip().split("=", 1)[1] for line in file if line.startswith(("POSTGRES_PASSWORD=", "FLEETOPS_DB_PASSWORD=")))
        for service in ("app", "postgres", "caddy"):
            result = subprocess.run(["docker", "logs", f"{ARGS.local_project}-{service}-1"], capture_output=True, check=True)
            logs = result.stdout + result.stderr
            self.assertTrue(all(secret.encode() not in logs for secret in secrets if secret), "credential appeared in private service logs")


if __name__ == "__main__":
    unittest.main(argv=["smoke.py"], verbosity=2)
