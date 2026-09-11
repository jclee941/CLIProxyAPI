from __future__ import annotations

import json
import os
import re
import sys
from collections import Counter
from dataclasses import dataclass
from enum import Enum
from http.client import HTTPException, HTTPResponse
from typing import Final, NewType, TypeAlias
from urllib.error import URLError
from urllib.request import HTTPHandler, OpenerDirector, ProxyHandler, Request


ENDPOINT: Final = "http://127.0.0.1:8317/v0/management/plugins/gemini-web/maintain"
BODY_LIMIT: Final = 65536
ID_PATTERN: Final = re.compile(r"gemini-web-[a-z0-9-]+\.json")
AuthID = NewType("AuthID", str)
JsonValue: TypeAlias = (
    "None | bool | int | float | str | list[JsonValue] | dict[str, JsonValue]"
)


class State(str, Enum):
    READY = "ready"
    BUSY_SKIP = "busy_skip"
    DISABLED_SKIP = "disabled_skip"
    COOLDOWN = "cooldown"
    SOURCE_REJECTED = "source_rejected"
    CREDENTIAL_ERROR = "credential_error"
    FENCED = "fenced"
    HOST_SYNC_PENDING = "host_sync_pending"
    NEEDS_OPERATOR = "needs_operator"


@dataclass(frozen=True, slots=True)
class Result:
    id: AuthID
    state: State


class InvalidResponse(ValueError):
    pass


def valid_id(value: str) -> bool:
    return len(value) <= 128 and ID_PATTERN.fullmatch(value) is not None


def parse_results(body: bytes) -> tuple[Result, ...]:
    if len(body) > BODY_LIMIT:
        raise InvalidResponse
    payload: JsonValue = json.loads(body)
    if not isinstance(payload, dict):
        raise InvalidResponse
    rows = payload.get("results")
    if not isinstance(rows, list):
        raise InvalidResponse
    results: list[Result] = []
    for row in rows:
        if not isinstance(row, dict):
            raise InvalidResponse
        auth_id, state = row.get("id"), row.get("state")
        if not isinstance(auth_id, str) or not valid_id(auth_id):
            raise InvalidResponse
        if not isinstance(state, str):
            raise InvalidResponse
        results.append(Result(AuthID(auth_id), State(state)))
    return tuple(results)


def main() -> int:
    arguments = sys.argv[1:]
    if arguments == ["--help"]:
        print("usage: maintain.py [--id gemini-web-<id>.json]")
        return 0
    body = b"{}"
    if arguments:
        if len(arguments) != 2 or arguments[0] != "--id" or not valid_id(arguments[1]):
            print("maintenance error=invalid_arguments", file=sys.stderr)
            return 2
        body = json.dumps({"id": arguments[1]}, separators=(",", ":")).encode("ascii")
    key = os.getenv("GEMINI_MAINTENANCE_KEY", "")
    if (
        not key
        or key.startswith("op://")
        or any(ord(char) < 33 or ord(char) > 126 for char in key)
    ):
        print("maintenance error=invalid_key", file=sys.stderr)
        return 2
    request = Request(
        ENDPOINT,
        data=body,
        method="POST",
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    opener = OpenerDirector()
    opener.add_handler(ProxyHandler({}))
    opener.add_handler(HTTPHandler())
    try:
        response: HTTPResponse = opener.open(request)
        with response:
            if not 200 <= response.status < 300:
                print(
                    f"maintenance error=http_status status={response.status}",
                    file=sys.stderr,
                )
                return 1
            raw = response.read(BODY_LIMIT + 1)
    except (URLError, OSError, HTTPException):
        print("maintenance error=request_failed", file=sys.stderr)
        return 1
    try:
        results = parse_results(raw)
    except (ValueError, UnicodeError, RecursionError):
        print("maintenance error=invalid_response", file=sys.stderr)
        return 1
    counts = Counter(result.state for result in results)
    summary = " ".join(f"{state.value}={counts[state]}" for state in State)
    print(f"maintenance results={len(results)} {summary}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
