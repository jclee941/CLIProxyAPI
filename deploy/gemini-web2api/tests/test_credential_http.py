from __future__ import annotations

import json
import subprocess
import sys
import threading
from collections.abc import Generator
from contextlib import closing
from http.client import HTTPConnection
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

import pytest

from extension import credential_worker
from extension.account import AccountError
from extension.credential_worker import Operation, WorkerError
from extension.credentials import encode_token
from extension.server import WebHandler
from tests.test_identity import DIGEST, GAIA
from tests.test_renewal import renewal_wire


@pytest.fixture
def bridge_port() -> Generator[int]:
    with ThreadingHTTPServer(("127.0.0.1", 0), WebHandler) as server:
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.start()
        try:
            yield server.server_port
        finally:
            server.shutdown()
            thread.join()


@pytest.mark.parametrize("operation", ["inspect", "renew"])
@pytest.mark.parametrize("error,status,kind", [
    (WorkerError("credential_timeout"), 504, "credential_timeout"),
    (WorkerError("credential_worker_failed"), 502, "credential_worker_failed"),
    (WorkerError("identity_unverified"), 412, "identity_unverified"),
    (AccountError("bootstrap_failed"), 502, "bootstrap_failed"),
    (AccountError("unauthenticated"), 401, "auth_error"),
    (AccountError("upstream_status", http_status=403), 403, "upstream_status"),
    (AccountError("upstream_status", http_status=429), 429, "upstream_status"),
    (AccountError("upstream_status", http_status=503), 503, "upstream_status"),
])
def test_http_when_credential_operation_fails_returns_fixed_error(
    bridge_port: int, operation: str, error: WorkerError | AccountError, status: int, kind: str,
) -> None:
    with patch("extension.server.run_credential_operation", side_effect=error), closing(HTTPConnection("127.0.0.1", bridge_port)) as client:
        client.request("POST", "/v1/session/" + operation, b"{}", {"x-goog-api-key": encode_token("SID=synthetic-private")})

        with client.getresponse() as response:
            assert response.status == status
            assert response.getheader("Cache-Control") == "no-store"
            assert json.loads(response.read()) == {"error": {"code": status, "message": kind}}


@pytest.mark.parametrize("headers", [
    [("Content-Length", "2"), ("Content-Length", "2")],
    [("Content-Length", "-1")],
    [("Content-Length", "32769")],
    [("Content-Length", "nonsense")],
    [("Transfer-Encoding", "chunked")],
])
@pytest.mark.parametrize("operation", ["inspect", "renew"])
def test_http_when_credential_framing_is_invalid_never_runs_worker(bridge_port: int, headers: list[tuple[str, str]], operation: str) -> None:
    with patch("extension.server.run_credential_operation") as worker, closing(HTTPConnection("127.0.0.1", bridge_port)) as client:
        client.putrequest("POST", "/v1/session/" + operation)
        client.putheader("x-goog-api-key", encode_token("SID=synthetic-private"))
        for name, value in headers:
            client.putheader(name, value)
        client.endheaders()

        with client.getresponse() as response:
            assert response.status == 400
            assert json.loads(response.read()) == {"error": {"code": 400, "message": "invalid_credential"}}
        worker.assert_not_called()


@pytest.mark.parametrize("path", ["/v1/account-models", "/v1/models", "/v1beta/models"])
def test_http_when_models_are_public_never_serializes_raw_identity(bridge_port: int, path: str) -> None:
    with renewal_wire(), patch("extension.server.NativeSession", new=credential_worker.HttpSession), closing(HTTPConnection("127.0.0.1", bridge_port)) as client:
        client.request("GET", path, headers={"x-goog-api-key": encode_token("SID=synthetic-private")})

        with client.getresponse() as response:
            payload = response.read()
            assert response.status == 200
            assert GAIA.encode() not in payload
            assert b"synthetic-private" not in payload
            if path == "/v1/account-models":
                assert json.loads(payload)["account_sha256"] == DIGEST
            else:
                assert DIGEST.encode() not in payload


@pytest.mark.parametrize("operation", ["inspect", "renew"])
def test_http_when_real_child_executes_direct_http_returns_verified_contract(bridge_port: int, operation: Operation) -> None:
    with renewal_wire() as upstream:
        upstream.cookies = ["SIDCC=synthetic-new; Domain=.google.com; Path=/; Secure"]
        script = f'''
import sys
from http.client import HTTPConnection
from extension import account, renewal, credential_worker
assert "extension.server" not in sys.modules
assert "extension.native" not in sys.modules
assert "extension.video" not in sys.modules
def connect(host: str, *, timeout: None) -> HTTPConnection:
    assert host in ("accounts.google.com", "gemini.google.com")
    assert timeout is None
    return HTTPConnection("127.0.0.1", {upstream.server_port}, timeout=None)
account.HTTPSConnection = connect
renewal.HTTPSConnection = connect
credential_worker.main()
'''
        original_popen = subprocess.Popen

        def local_child(command: list[str], *, stdin: int, stdout: int, stderr: int, cwd: Path) -> subprocess.Popen[bytes]:
            assert command == [sys.executable, "-m", "extension.credential_worker"]
            assert stderr == subprocess.DEVNULL
            return original_popen([sys.executable, "-c", script], stdin=stdin, stdout=stdout, stderr=stderr, cwd=cwd)

        with patch("extension.credential_worker.subprocess.Popen", side_effect=local_child), closing(HTTPConnection("127.0.0.1", bridge_port)) as client:
            client.request("POST", "/v1/session/" + operation, b"{}", {"x-goog-api-key": encode_token("SID=synthetic-private", 2)})

            with client.getresponse() as response:
                result = json.loads(response.read())
                assert response.status == 200
                assert result["account_sha256"] == DIGEST
                assert result["auth_user"] == 2
                assert response.getheader("Cache-Control") == "no-store"
                if operation == "renew":
                    assert result["token"] == encode_token("SID=synthetic-private; SIDCC=synthetic-new", 2)
                else:
                    assert "token" not in result
        assert len(upstream.requests) == (5 if operation == "renew" else 2)
