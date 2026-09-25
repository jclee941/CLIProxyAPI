from __future__ import annotations

import json
import os
import subprocess
import sys
from unittest.mock import patch

import pytest

from extension import credential_worker as worker
from extension.account import AccountError, AccountModels
from extension.credentials import encode_token
from tests.test_identity import DIGEST

TOKEN = encode_token("SID=synthetic-worker", 2)


@pytest.mark.parametrize("operation", ["inspect", "renew"])
@pytest.mark.parametrize("status", [None, 1000])
def test_worker_when_authenticated_returns_only_verified_identity(operation: worker.Operation, status: int | None) -> None:
    models = AccountModels((), (), status, 1, account_sha256=DIGEST)
    replacement = encode_token("SID=synthetic-new", 2)
    with patch.object(worker.HttpSession, "account_models", return_value=models), patch.object(worker, "renew_session", return_value=replacement) as renew:
        result = worker.execute(operation, TOKEN)

    assert result == ({"account_sha256": DIGEST, "auth_user": 2} if operation == "inspect" else {"account_sha256": DIGEST, "auth_user": 2, "token": replacement})
    assert renew.call_count == (operation == "renew")


@pytest.mark.parametrize("models", [
    AccountModels((), (), 1000, 1),
    AccountModels((), (), None, 1),
])
@pytest.mark.parametrize("operation", ["inspect", "renew"])
def test_worker_when_identity_is_unverified_rejects(models: AccountModels, operation: worker.Operation) -> None:
    with patch.object(worker.HttpSession, "account_models", return_value=models), patch.object(worker, "renew_session", return_value=TOKEN), pytest.raises(worker.WorkerError) as caught:
        _ = worker.execute(operation, TOKEN)

    assert (caught.value.kind, caught.value.status) == ("identity_unverified", 412)


@pytest.mark.parametrize("error,kind,status", [
    (AccountError("unauthenticated"), "auth_error", 401),
    (AccountError("rpc_denied", provider_code=1016), "auth_error", 401),
    (AccountError("bootstrap_failed"), "bootstrap_failed", 502),
    (AccountError("invalid_response"), "invalid_response", 502),
    (AccountError("transport_failed"), "transport_failed", 502),
    (AccountError("upstream_status", http_status=403), "upstream_status", 403),
    (AccountError("upstream_status", http_status=429), "upstream_status", 429),
    (AccountError("upstream_status", http_status=503), "upstream_status", 503),
])
def test_worker_when_account_fails_preserves_safe_classification(error: AccountError, kind: str, status: int) -> None:
    with patch.object(worker.HttpSession, "account_models", side_effect=error):
        response = worker.process_request(json.dumps({"operation": "inspect", "token": TOKEN}).encode())

    assert response == {"error": {"code": status, "message": kind}}


@pytest.mark.parametrize("payload", [b"{}", b"[]", b'{}{}', b'{"operation":"generate","token":"secret"}', b'{"operation":"inspect","operation":"renew","token":"secret"}', b'{"operation":"inspect","token":"secret","extra":0}', b"x" * 66000])
def test_worker_when_input_is_invalid_never_calls_provider(payload: bytes) -> None:
    with patch.object(worker.HttpSession, "account_models") as models:
        result = worker.process_request(payload)

    assert result == {"error": {"code": 400, "message": "invalid_credential"}}
    models.assert_not_called()


def test_parent_when_running_worker_uses_stdin_and_fixed_budget() -> None:
    reply = {"account_sha256": DIGEST, "auth_user": 2}
    with patch.object(worker.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, json.dumps(reply).encode())) as run:
        result = worker.run_credential_operation("inspect", TOKEN)

    assert result == reply
    assert run.call_args.args == ([sys.executable, "-m", "extension.credential_worker"],)
    assert json.loads(run.call_args.kwargs["input"]) == {"operation": "inspect", "token": TOKEN}
    assert run.call_args.kwargs["timeout"] == 60
    assert run.call_args.kwargs["stderr"] == subprocess.DEVNULL
    assert "env" not in run.call_args.kwargs


@pytest.mark.parametrize("stdout,returncode", [
    (b"private malformed output", 0), (b"x" * 65537, 0), (b"{}", 1),
    (b'{"error":{"code":401,"message":"private third-party message"}}', 0),
    (b'{"account_sha256":"bad","auth_user":2}', 0),
    (json.dumps({"account_sha256": DIGEST, "auth_user": True}).encode(), 0),
    (json.dumps({"account_sha256": DIGEST, "auth_user": 3}).encode(), 0),
    (json.dumps({"account_sha256": DIGEST, "auth_user": 2, "token": TOKEN}).encode(), 0),
])
def test_parent_when_worker_output_is_invalid_sanitizes(stdout: bytes, returncode: int) -> None:
    with patch.object(worker.subprocess, "run", return_value=subprocess.CompletedProcess([], returncode, stdout)), pytest.raises(worker.WorkerError) as caught:
        _ = worker.run_credential_operation("inspect", TOKEN)

    assert (caught.value.kind, caught.value.status) == ("credential_worker_failed", 502)
    assert "private" not in str(caught.value)


@pytest.mark.parametrize("reply", [
    {"account_sha256": DIGEST, "auth_user": 2},
    {"account_sha256": DIGEST, "auth_user": 2, "token": "not-a-token"},
    {"account_sha256": DIGEST, "auth_user": 2, "token": encode_token("SID=synthetic", 3)},
    {"account_sha256": DIGEST, "auth_user": 2, "token": "x" * 32769},
    {"account_sha256": DIGEST, "auth_user": 2, "token": TOKEN, "private": "hidden"},
])
def test_parent_when_renewal_reply_has_invalid_token_rejects(reply: dict[str, str | int]) -> None:
    with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, json.dumps(reply).encode())), pytest.raises(worker.WorkerError) as caught:
        _ = worker.run_credential_operation("renew", TOKEN)

    assert (caught.value.kind, caught.value.status) == ("credential_worker_failed", 502)


@pytest.mark.parametrize("kind,status", [("auth_error", 401), ("identity_unverified", 412), ("upstream_status", 403), ("upstream_status", 503), ("bootstrap_failed", 502)])
def test_parent_when_worker_reports_known_error_preserves_status(kind: str, status: int) -> None:
    payload = json.dumps({"error": {"code": status, "message": kind}}).encode()
    with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, payload)), pytest.raises(worker.WorkerError) as caught:
        _ = worker.run_credential_operation("inspect", TOKEN)

    assert (caught.value.kind, caught.value.status) == (kind, status)


def test_parent_when_process_cannot_start_returns_safe_failure() -> None:
    with patch.object(subprocess, "run", side_effect=OSError("synthetic-private-start-failure")), pytest.raises(worker.WorkerError) as caught:
        _ = worker.run_credential_operation("inspect", TOKEN)

    assert (caught.value.kind, caught.value.status) == ("credential_worker_failed", 502)


@pytest.mark.parametrize("token,status", [("legacy-key", 401), ("", 401), ("x" * 32769, 431)])
def test_worker_when_token_is_invalid_does_not_contact_provider(token: str, status: int) -> None:
    with patch.object(worker.HttpSession, "account_models") as models:
        result = worker.process_request(json.dumps({"operation": "inspect", "token": token}).encode())

    assert "error" in result and result["error"]["code"] == status
    models.assert_not_called()


def test_parent_when_child_hangs_kills_and_reaps_before_timeout_reply() -> None:
    processes: list[subprocess.Popen[bytes]] = []
    original_popen = subprocess.Popen

    def sleeping_child(*args: str, **kwargs: int) -> subprocess.Popen[bytes]:
        process = original_popen([sys.executable, "-c", "import time; time.sleep(100)"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        processes.append(process)
        return process

    with patch.object(worker.subprocess, "Popen", sleeping_child), patch.object(worker, "CREDENTIAL_WORKER_BUDGET", 0.1), pytest.raises(worker.WorkerError) as caught:
        _ = worker.run_credential_operation("inspect", TOKEN)

    assert (caught.value.kind, caught.value.status) == ("credential_timeout", 504)
    assert len(processes) == 1
    assert processes[0].returncode == -9
    with pytest.raises(ProcessLookupError):
        os.kill(processes[0].pid, 0)
    with pytest.raises(ChildProcessError):
        _ = os.waitpid(processes[0].pid, os.WNOHANG)


def test_worker_cli_when_invalid_operation_is_safe_and_has_no_server_import() -> None:
    result = subprocess.run([sys.executable, "-m", "extension.credential_worker"], input=b'{"operation":"generate","token":"synthetic-secret"}', capture_output=True, timeout=5, check=False)

    assert result.returncode == 0
    assert json.loads(result.stdout) == {"error": {"code": 400, "message": "invalid_credential"}}
    assert result.stderr == b""
