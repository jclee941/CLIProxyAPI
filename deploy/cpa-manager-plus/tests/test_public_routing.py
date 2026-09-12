import json
import re
import subprocess
import unittest
from pathlib import Path
from urllib.parse import urlsplit


ROOT = Path(__file__).resolve().parents[1]


class PublicRoutingTests(unittest.TestCase):
    def test_loopback_gateway_has_no_active_response_deadlines(self) -> None:
        static = json.loads((ROOT / "gateway.yaml").read_text())
        dynamic = json.loads((ROOT / "gateway-routes.yaml").read_text())
        self.assertEqual(static["entryPoints"]["gateway"]["address"], "127.0.0.1:8317")
        self.assertEqual(set(static["entryPoints"]), {"gateway"})
        for entry in static["entryPoints"].values():
            self.assertEqual(entry["transport"]["respondingTimeouts"]["readTimeout"], "0s")
            self.assertEqual(entry["transport"]["respondingTimeouts"]["writeTimeout"], "0s")
        self.assertEqual(dynamic["http"]["services"]["core"]["loadBalancer"]["servers"], [{"url":"http://127.0.0.1:18318"}])
        self.assertEqual(dynamic["http"]["services"]["manager"]["loadBalancer"]["servers"], [{"url":"http://127.0.0.1:18317"}])
        self.assertIn("Host(`cliproxy.jclee.me`)", dynamic["http"]["routers"]["manager"]["rule"])
        self.assertNotIn("Host(", dynamic["http"]["routers"]["core"]["rule"])
        self.assertEqual(static["serversTransport"]["forwardingTimeouts"]["responseHeaderTimeout"], "0s")
        self.assertNotIn("api", static)
        self.assertNotIn("accessLog", static)

    def test_manager_routes_replace_dashboard_without_intercepting_model_apis(self) -> None:
        config = json.loads((ROOT / "gateway-routes.yaml").read_text())["http"]
        match = re.search(r"PathRegexp\(`(.+)`\)", config["routers"]["manager"]["rule"])
        self.assertIsNotNone(match)
        assert match is not None
        manager_path = match.group(1)

        def target(url: str) -> str | None:
            parsed = urlsplit(url)
            service = "manager" if parsed.hostname == "cliproxy.jclee.me" and re.search(manager_path, parsed.path) else "core"
            return config["services"][service]["loadBalancer"]["servers"][0]["url"]

        for path in (
            "/", "/management.html", "/health", "/status", "/setup", "/models",
            "/usage-service/info", "/usage-service/config",
            "/v0/management/config", "/v0/management/usage",
            "/v0/management/plugins/gemini-web/accounts",
            "/v0/resource/plugins/gemini-web/index",
        ):
            with self.subTest(manager_path=path):
                self.assertEqual(target("https://cliproxy.jclee.me" + path), "http://127.0.0.1:18317")
        for path in (
            "/v1/models", "/v1/chat/completions", "/v1/responses",
            "/v1/images/generations", "/v1/messages",
            "/v1beta/models/gemini-web-omni:generateContent", "/v1internal:generateContent",
            "/api/provider/claude/v1/messages", "/ws", "/codex/callback",
            "/management.html-malformed", "/v0/management-invalid",
        ):
            with self.subTest(core_path=path):
                self.assertEqual(target("https://cliproxy.jclee.me" + path), "http://127.0.0.1:18318")
        self.assertEqual(target("http://127.0.0.1:8317/v0/management/config"), "http://127.0.0.1:18318")

    def test_public_manager_origin_preserves_existing_lan_origins(self) -> None:
        result = subprocess.run(
            ["docker", "compose", "--profile", "approval-required", "-f", str(ROOT / "compose.yml"), "config", "--format", "json"],
            check=True, capture_output=True,
        )
        config = json.loads(result.stdout)
        origins = set(config["services"]["cpa-manager-plus"]["environment"]["USAGE_CORS_ORIGINS"].split(","))
        self.assertIn("https://cliproxy.jclee.me", origins)
        self.assertIn("http://192.168.50.114:18317", origins)
        self.assertNotIn("*", origins)


if __name__ == "__main__":
    unittest.main()
