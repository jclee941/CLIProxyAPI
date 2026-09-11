"""Run with python3 -m unittest discover -s ops/tests -v."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Final, final, override


OPS: Final = Path(__file__).resolve().parents[1]
RUNNER: Final = OPS / "maintain.py"
AUTH_ID: Final = "gemini-web-synthetic-2.json"
SECRET: Final = "synthetic-management-key-DO-NOT-PRINT"
STATES: Final = (
    "ready",
    "busy_skip",
    "disabled_skip",
    "cooldown",
    "source_rejected",
    "credential_error",
    "fenced",
    "host_sync_pending",
    "needs_operator",
)
PROBE: Final = r"""
import os, sys
sys.path.insert(0, sys.argv[1])
import maintain
key = os.environ.get('GEMINI_MAINTENANCE_KEY')
assert not key or key not in '\0'.join(sys.orig_argv)
maintain.ENDPOINT = sys.argv[2]
sys.argv = [str(maintain.__file__), *sys.argv[3:]]
raise SystemExit(maintain.main())
"""


@final
class RunnerTests(unittest.TestCase):
    """A mutable wire fixture records every request from each fresh subprocess."""

    def __init__(self, methodName: str = "runTest") -> None:
        super().__init__(methodName)
        self.requests: list[tuple[str, str, str | None, str | None, bytes]] = []
        self.status = 200
        self.drop_response = False
        self.body = json.dumps(
            {"results": [{"id": AUTH_ID, "state": "ready"}]}
        ).encode()
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        fixture = self

        @final
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                fixture.requests.append(
                    (
                        self.command,
                        self.path,
                        self.headers.get("Authorization"),
                        self.headers.get("Content-Type"),
                        self.rfile.read(int(self.headers.get("Content-Length", "0"))),
                    )
                )
                if fixture.drop_response:
                    self.close_connection = True
                    return
                self.send_response(fixture.status)
                self.send_header(
                    "Location", f"http://127.0.0.1:{fixture.server.server_port}/leak"
                )
                self.send_header("Content-Length", str(len(fixture.body)))
                self.end_headers()
                _ = self.wfile.write(fixture.body)

            def do_GET(self) -> None:
                self.do_POST()

            @override
            def log_message(self, format: str, *args: str | int) -> None:
                return

        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        self.addCleanup(self.server.server_close)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.addCleanup(self.thread.join)
        self.addCleanup(self.server.shutdown)
        self.endpoint = (
            f"http://127.0.0.1:{self.server.server_port}"
            "/v0/management/plugins/gemini-web/maintain"
        )

    @override
    def setUp(self) -> None:
        self.thread.start()

    def invoke(
        self, args: tuple[str, ...] = (), key: str | None = SECRET
    ) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        _ = environment.pop("GEMINI_MAINTENANCE_KEY", None)
        if key is not None:
            environment["GEMINI_MAINTENANCE_KEY"] = key
        environment.update(
            {
                "http_proxy": "http://127.0.0.1:1",
                "HTTP_PROXY": "http://127.0.0.1:1",
                "https_proxy": "http://127.0.0.1:1",
                "ALL_PROXY": "http://127.0.0.1:1",
                "no_proxy": "",
                "NO_PROXY": "",
                "PYTHONDONTWRITEBYTECODE": "1",
            }
        )
        command = [sys.executable, "-c", PROBE, str(OPS), self.endpoint, *args]
        self.assertNotIn(SECRET, "\0".join(command))
        return subprocess.run(
            command,
            env=environment,
            cwd=self.directory.name,
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )

    def test_exact_request_when_default_cycle_ignores_proxy_and_stale_env(self) -> None:
        """Given a stale .env; when invoked; then only the injected key is sent."""
        _ = Path(self.directory.name, ".env").write_text(
            "GEMINI_MAINTENANCE_KEY=stale-key\n"
        )
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            self.requests,
            [
                (
                    "POST",
                    "/v0/management/plugins/gemini-web/maintain",
                    f"Bearer {SECRET}",
                    "application/json",
                    b"{}",
                )
            ],
        )
        self.assertNotIn(SECRET, result.stdout + result.stderr)

    def test_scoped_request_when_explicit_id(self) -> None:
        """Given a valid auth ID; when selected; then exactly that ID is posted."""
        result = self.invoke(("--id", AUTH_ID))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.requests), 1)
        self.assertEqual(json.loads(self.requests[0][4]), {"id": AUTH_ID})

    def test_fresh_processes_when_two_invocations(self) -> None:
        """Given two injected keys; when separate jobs run; then neither key is cached."""
        results = [self.invoke(key=key) for key in (SECRET, "second-synthetic-key")]
        self.assertEqual([result.returncode for result in results], [0, 0])
        self.assertEqual(
            [request[2] for request in self.requests],
            [
                f"Bearer {SECRET}",
                "Bearer second-synthetic-key",
            ],
        )

    def test_no_request_when_key_missing_empty_unresolved_or_invalid(self) -> None:
        """Given invalid runtime credentials and a .env; when run; then no HTTP occurs."""
        _ = Path(self.directory.name, ".env").write_text(
            f"GEMINI_MAINTENANCE_KEY={SECRET}\n"
        )
        for key in (
            None,
            "",
            "op://homelab/synthetic/management_key",
            "bad\nheader",
            "bad\u2603",
        ):
            with self.subTest(key_present=key is not None):
                result = self.invoke(key=key)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertEqual(result.stderr, "maintenance error=invalid_key\n")
        self.assertEqual(self.requests, [])

    def test_no_retry_or_redirect_when_http_failure(self) -> None:
        """Given error/redirect bodies; when posted; then no retry, GET, or body logging occurs."""
        self.body = SECRET.encode()
        for status in (301, 302, 303, 307, 308, 401, 403, 429, 500, 503):
            with self.subTest(status=status):
                self.status = status
                self.requests.clear()
                result = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(self.requests), 1)
                self.assertEqual(result.stdout, "")
                self.assertEqual(
                    result.stderr, f"maintenance error=http_status status={status}\n"
                )

    def test_no_partial_summary_when_response_malformed(self) -> None:
        """Given untrusted JSON; when parsed; then only a fixed error is printed."""
        for body in (
            SECRET.encode(),
            b"[]",
            b"{}",
            b'{"results":null}',
            json.dumps({"results": [{"id": SECRET, "state": "ready"}]}).encode(),
            json.dumps({"results": [{"id": AUTH_ID, "state": SECRET}]}).encode(),
            b'{"results":[{"id":"gemini-web-ok.json","state":"ready"},null]}',
            b"x" * 65537,
        ):
            with self.subTest(size=len(body)):
                self.body = body
                self.requests.clear()
                result = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(self.requests), 1)
                self.assertEqual(result.stdout, "")
                self.assertEqual(result.stderr, "maintenance error=invalid_response\n")

    def test_no_retry_when_response_lost(self) -> None:
        self.drop_response = True
        result = self.invoke()
        self.assertEqual(len(self.requests), 1)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr, "maintenance error=request_failed\n")

    def test_summary_only_when_untrusted_optional_fields(self) -> None:
        """Given every state plus hostile optional fields; when run; then only state counts escape."""
        self.body = json.dumps(
            {
                "results": [
                    {
                        "id": AUTH_ID,
                        "state": state,
                        "error": SECRET,
                        "next_due_at": SECRET,
                        "token": SECRET,
                        "email": "person@example.invalid",
                    }
                    for state in STATES
                ],
                "debug": SECRET,
            }
        ).encode()
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout,
            "maintenance results=9 "
            + " ".join(f"{state}=1" for state in STATES)
            + "\n",
        )
        self.assertEqual(result.stderr, "")

    def test_safe_usage_when_invalid_cli_input(self) -> None:
        """Given invalid IDs/options; when parsed; then inputs are never echoed or sent."""
        for args in (
            ("--id", "untrusted-input"),
            ("--id", "gemini-web-a\n.json"),
            ("--id", "gemini-web-" + "a" * 256 + ".json"),
            ("--id", "person@example.invalid"),
            ("--id",),
            ("--key", "untrusted-input"),
            ("--id", AUTH_ID, "extra"),
        ):
            with self.subTest(argument_count=len(args)):
                result = self.invoke(args)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stderr, "maintenance error=invalid_arguments\n")
                self.assertEqual(result.stdout, "")
        self.assertEqual(self.requests, [])

    def test_help_when_direct_script_has_no_key(self) -> None:
        """Given the production entry point; when help is requested; then no credential is needed."""
        result = subprocess.run(
            [sys.executable, str(RUNNER), "--help"],
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--id", result.stdout)
        self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    _ = unittest.main()
