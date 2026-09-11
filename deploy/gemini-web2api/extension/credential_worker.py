"""Private credential process protocol; generation and server startup are excluded."""

from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Final, Literal, NotRequired, TypedDict, assert_never, override

from .account import AccountError, ErrorKind, HttpSession
from .credentials import (
    MAX_TOKEN_LENGTH,
    CredentialError,
    CredentialErrorKind,
    _decode_json,
    decode_token,
)
from .renewal import renew_session

type Operation = Literal["inspect", "renew"]
type SafeKind = ErrorKind | CredentialErrorKind | Literal[
    "auth_error", "identity_unverified", "credential_timeout", "credential_worker_failed"
]
CREDENTIAL_WORKER_BUDGET: Final = 60
MAX_WORKER_INPUT: Final = MAX_TOKEN_LENGTH + 128
MAX_WORKER_OUTPUT: Final = MAX_TOKEN_LENGTH + 32768
_STATUSES: Final[dict[SafeKind, int]] = {
    "auth_error": 401, "identity_unverified": 412,
    "credential_timeout": 504, "credential_worker_failed": 502,
    "transport_failed": 502, "bootstrap_failed": 502, "invalid_response": 502,
    "upstream_status": 502, "rpc_denied": 403, "account_unavailable": 502,
    "model_unavailable": 404, "invalid_config": 502,
    "missing_credential": 401, "unknown_credential": 401,
    "invalid_credential": 400, "credential_too_long": 431,
}


class CredentialReply(TypedDict):
    account_sha256: str
    auth_user: int
    token: NotRequired[str]


class ErrorDetail(TypedDict):
    code: int
    message: str


class ErrorReply(TypedDict):
    error: ErrorDetail


class WorkerError(Exception):
    def __init__(self, kind: SafeKind, status: int | None = None) -> None:
        self.kind: SafeKind = kind
        self.status: int = _STATUSES[kind] if status is None else status
        super().__init__(kind, self.status)

    @override
    def __str__(self) -> str:
        return f"{self.kind} (status={self.status})"


def account_failure(error: AccountError) -> WorkerError:
    if error.kind == "unauthenticated" or (
        error.kind == "rpc_denied" and error.provider_code == 1016
    ):
        return WorkerError("auth_error")
    status = error.http_status
    if error.kind == "upstream_status" and status is not None and 300 <= status <= 599:
        return WorkerError(error.kind, status)
    return WorkerError(error.kind)


def execute(operation: Operation, token: str) -> CredentialReply:
    credential = decode_token(token)
    match operation:
        case "inspect":
            verified_token = token
        case "renew":
            verified_token = renew_session(credential)
        case unreachable:
            assert_never(unreachable)
    verified = decode_token(verified_token)
    models = HttpSession(verified.cookie, str(verified.auth_user)).account_models()
    if models.account_sha256 is None:
        raise WorkerError("identity_unverified")
    reply: CredentialReply = {"account_sha256": models.account_sha256, "auth_user": verified.auth_user}
    if operation == "renew":
        reply["token"] = verified_token
    return reply


def process_request(payload: bytes) -> CredentialReply | ErrorReply:
    try:
        if len(payload) > MAX_WORKER_INPUT:
            raise CredentialError("invalid_credential", 400)
        value = _decode_json(payload)
        match value:
            case {"operation": ("inspect" | "renew") as operation, "token": str() as token} if len(value) == 2:
                return execute(operation, token)
            case _:
                raise CredentialError("invalid_credential", 400)
    except AccountError as error:
        failure = account_failure(error)
    except CredentialError as error:
        failure = WorkerError(error.kind, error.status)
    except WorkerError as error:
        failure = error
    except (ValueError, UnicodeError, RecursionError):
        failure = WorkerError("invalid_credential")
    return {"error": {"code": failure.status, "message": failure.kind}}


def _parse_reply(raw: bytes, operation: Operation) -> CredentialReply:
    if len(raw) > MAX_WORKER_OUTPUT:
        raise WorkerError("credential_worker_failed")
    try:
        value = _decode_json(raw)
    except (ValueError, UnicodeError, RecursionError, CredentialError):
        raise WorkerError("credential_worker_failed") from None
    match value:
        case {"error": {"code": int() as status, "message": str() as message} as detail} if len(value) == 1 and len(detail) == 2:
            for kind, expected in _STATUSES.items():
                if message == kind and type(status) is int and (
                    status == expected or (kind == "upstream_status" and 300 <= status <= 599)
                ):
                    raise WorkerError(kind, status)
        case {"account_sha256": str() as digest, "auth_user": int() as auth_user}:
            fields = {"account_sha256", "auth_user"} | ({"token"} if operation == "renew" else set[str]())
            if value.keys() == fields and re.fullmatch(r"[0-9a-f]{64}", digest) and type(auth_user) is int and auth_user >= 0:
                reply: CredentialReply = {"account_sha256": digest, "auth_user": auth_user}
                if operation == "inspect":
                    return reply
                match value.get("token"):
                    case str() as token:
                        try:
                            renewed = decode_token(token)
                        except CredentialError:
                            raise WorkerError("credential_worker_failed") from None
                        if renewed.auth_user == auth_user:
                            reply["token"] = token
                            return reply
    raise WorkerError("credential_worker_failed")


def run_credential_operation(operation: Operation, token: str) -> CredentialReply:
    credential = decode_token(token)
    payload = json.dumps({"operation": operation, "token": token}).encode("utf-8")
    try:
        completed = subprocess.run(
            [sys.executable, "-m", "extension.credential_worker"],
            input=payload, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            timeout=CREDENTIAL_WORKER_BUDGET, check=False,
            cwd=Path(__file__).resolve().parent.parent,
        )
    except subprocess.TimeoutExpired:
        raise WorkerError("credential_timeout") from None
    except OSError:
        raise WorkerError("credential_worker_failed") from None
    if completed.returncode != 0:
        raise WorkerError("credential_worker_failed")
    result = _parse_reply(completed.stdout, operation)
    if result["auth_user"] != credential.auth_user:
        raise WorkerError("credential_worker_failed")
    return result


def main() -> None:
    try:
        reply = process_request(sys.stdin.buffer.read(MAX_WORKER_INPUT + 1))
        output = json.dumps(reply, separators=(",", ":")).encode("utf-8")
        if len(output) > MAX_WORKER_OUTPUT:
            raise WorkerError("credential_worker_failed")
    except WorkerError:
        output = b'{"error":{"code":502,"message":"credential_worker_failed"}}'
    _ = sys.stdout.buffer.write(output)


if __name__ == "__main__":
    main()
