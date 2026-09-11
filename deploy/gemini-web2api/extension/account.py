"""Direct Gemini Web account RPCs, without generation or credential persistence.

Usage quantities are provider compute units, not tokens, requests, or currency.
Only the observed PRO tier and metric window codes are named. Unknown numeric
codes remain available to callers; missing or unrecognized values remain None.
"""

from __future__ import annotations

import hashlib
import json
import math
import re
import time
from collections.abc import Callable
from contextlib import closing
from dataclasses import dataclass
from http.client import HTTPConnection, HTTPException, HTTPSConnection
from typing import Final, Literal, override
from urllib.parse import urlencode, urlsplit

type JsonValue = (
    str | int | float | bool | None | list[JsonValue] | dict[str, JsonValue]
)
type ErrorKind = Literal[
    "invalid_config",
    "transport_failed",
    "upstream_status",
    "bootstrap_failed",
    "invalid_response",
    "rpc_denied",
    "unauthenticated",
    "account_unavailable",
    "model_unavailable",
]
type WindowKind = Literal["5h", "weekly", "ai_credit", "unknown"]

_MODEL_HEADER: Final = "[1,null,null,null,null,null,null,null,[4,5,6,8],null,null,null,null,null,null,null]"


class AccountError(Exception):
    """Safe boundary error: only fixed categories and numeric status codes escape."""

    def __init__(
        self,
        kind: ErrorKind,
        *,
        http_status: int | None = None,
        provider_code: int | None = None,
    ) -> None:
        self.kind: ErrorKind = kind
        self.http_status: int | None = http_status
        self.provider_code: int | None = provider_code
        super().__init__(kind, http_status, provider_code)

    @override
    def __str__(self) -> str:
        return f"Gemini Web {self.kind} (status={self.http_status}, code={self.provider_code})"


@dataclass(frozen=True, slots=True)
class ModelCapability:
    capability_id: str
    display_name: str
    mode: int


@dataclass(frozen=True, slots=True)
class AccountModels:
    models: tuple[ModelCapability, ...]
    capacity_flags: tuple[int | None, ...] | None
    status_code: int | None
    observed_at: float
    available: Literal[True] = True
    source: Literal["GoogleWeb"] = "GoogleWeb"
    estimated: Literal[False] = False

    def select_model(self, display_name: str) -> ModelCapability:
        matches = tuple(
            model for model in self.models if model.display_name == display_name
        )
        if len(matches) != 1:
            raise AccountError("model_unavailable")
        return matches[0]


@dataclass(frozen=True, slots=True)
class UsageMetric:
    """Provider measurements; reset_at is Unix seconds and fractions are unrounded."""

    remaining_units: int | float | None
    usage_fraction: int | float | None
    metric_type: int | None
    reset_at: int | float | None
    window_kind: WindowKind
    unit: Literal["provider_compute_unit"] = "provider_compute_unit"

    @property
    def usage_percent(self) -> int | float | None:
        return None if self.usage_fraction is None else self.usage_fraction * 100


@dataclass(frozen=True, slots=True)
class AccountUsage:
    tier_code: int | None
    tier: Literal["PRO"] | None
    overage: bool | None
    metrics: tuple[UsageMetric, ...] | None
    observed_at: float
    source: Literal["GoogleWeb"] = "GoogleWeb"
    estimated: Literal[False] = False


def _field(value: JsonValue, *path: int) -> JsonValue:
    current = value
    for index in path:
        match current:
            case list() if index < len(current):
                current = current[index]
            case list() | str() | int() | float() | dict() | None:
                return None
    return current


def _number(value: JsonValue) -> int | float | None:
    match value:
        case bool():
            return None
        case int():
            return value
        case float():
            return value if math.isfinite(value) else None
        case str() | list() | dict() | None:
            return None


def _integer(value: JsonValue) -> int | None:
    number = _number(value)
    match number:
        case int():
            return number
        case float() | None:
            return None


def _array(value: JsonValue) -> list[JsonValue]:
    match value:
        case list():
            return value
        case str() | int() | float() | dict() | None:
            raise AccountError("invalid_response")


def _json(
    data: str | bytes, decode: Callable[[str | bytes], JsonValue] = json.loads
) -> JsonValue:
    try:
        return decode(data)
    except (ValueError, UnicodeError, RecursionError):
        raise AccountError("invalid_response") from None


def _decode_rpc(data: bytes, rpcid: str) -> JsonValue:
    remaining = data.removeprefix(b")]}'").lstrip()
    encoded_results: list[str] = []
    while remaining:
        size_line, separator, remainder = remaining.partition(b"\n")
        if not separator or not size_line.isdigit():
            raise AccountError("invalid_response")
        try:
            size = int(size_line)
        except ValueError:
            raise AccountError("invalid_response") from None
        frame_data, _, rest = remainder.partition(b"\n")
        try:
            frame_units = len(frame_data.decode("utf-8").encode("utf-16-le")) // 2
        except UnicodeError:
            raise AccountError("invalid_response") from None
        if size not in (frame_units, frame_units + 2):
            raise AccountError("invalid_response")
        frames = _array(_json(frame_data))
        remaining = rest.lstrip()
        for frame in frames:
            if _field(frame, 0) != "wrb.fr" or _field(frame, 1) != rpcid:
                continue
            rejection = _field(frame, 5, 0)
            if rejection is not None:
                raise AccountError("rpc_denied", provider_code=_integer(rejection))
            match _field(frame, 2):
                case str() as encoded:
                    encoded_results.append(encoded)
                case int() | float() | list() | dict() | None:
                    raise AccountError("invalid_response")
    if len(encoded_results) != 1:
        raise AccountError("invalid_response")
    return _json(encoded_results[0])


class HttpSession:
    """Synchronous, single-owner session; bootstrap state and cookies stay in memory.

    An explicit upstream_url supports isolated HTTP tests. Redirects are never
    followed, so cookies cannot be forwarded to another origin. Requests have
    no retries or network deadlines. bootstrap() returns no credential material.
    rpc() is the low-level wire boundary for sibling RPC adapters; its decoded
    payload must not be exposed by public routes. Account methods return only
    the frozen, allowlisted domain values above.
    """

    def __init__(
        self,
        cookie: str,
        account_index: str | None = None,
        *,
        upstream_url: str = "https://gemini.google.com",
    ) -> None:
        try:
            origin = urlsplit(upstream_url)
            _ = origin.port
        except ValueError:
            raise AccountError("invalid_config") from None
        if (
            origin.scheme not in ("http", "https")
            or not origin.hostname
            or origin.username
            or origin.password
            or origin.query
            or origin.fragment
            or origin.path not in ("", "/")
            or not cookie
            or any(ord(char) < 32 or ord(char) > 255 for char in cookie)
            or (
                account_index is not None and not re.fullmatch(r"[0-9]+", account_index)
            )
        ):
            raise AccountError("invalid_config")
        self._origin: str = f"{origin.scheme}://{origin.netloc}"
        self._cookie: str = cookie
        self._sapisid: str = ""
        for pair in cookie.split(";"):
            name, _, value = pair.strip().partition("=")
            if name == "SAPISID":
                self._sapisid = value
        self._prefix: str = "" if account_index in (None, "0") else f"/u/{account_index}"
        self._xsrf: str | None = None
        self._build: str | None = None
        self._session_id: str | None = None
        self._request_id: int = 100000

    def _request_headers(self) -> dict[str, str]:
        headers = {
            "Cookie": self._cookie,
            "Origin": self._origin,
            "Referer": self._origin + self._prefix + "/app",
            "X-Same-Domain": "1",
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
            "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
            "x-goog-ext-525001261-jspb": _MODEL_HEADER,
            "x-goog-ext-73010989-jspb": "[0]",
        }
        if self._prefix:
            headers["X-Goog-AuthUser"] = self._prefix.removeprefix("/u/")
        if self._sapisid:
            timestamp = int(time.time())
            digest = hashlib.sha1(
                f"{timestamp} {self._sapisid} {self._origin}".encode()
            ).hexdigest()
            headers["Authorization"] = f"SAPISIDHASH {timestamp}_{digest}"
        return headers

    def _request(self, path: str, body: bytes | None = None) -> bytes:
        origin = urlsplit(self._origin)
        connection_type = (
            HTTPSConnection if origin.scheme == "https" else HTTPConnection
        )
        headers = self._request_headers()
        try:
            with closing(connection_type(origin.netloc, timeout=None)) as connection:
                connection.request(
                    "GET" if body is None else "POST", path, body, headers
                )
                with connection.getresponse() as response:
                    if not 200 <= response.status < 300:
                        raise AccountError(
                            "upstream_status", http_status=response.status
                        )
                    return response.read()
        except (OSError, HTTPException, ValueError):
            raise AccountError("transport_failed") from None

    def bootstrap(self) -> None:
        self._xsrf = self._build = self._session_id = None
        try:
            page = self._request(self._prefix + "/app").decode("utf-8")
        except UnicodeError:
            raise AccountError("bootstrap_failed") from None
        values: dict[str, str] = {}
        for key in ("SNlM0e", "cfb2h", "FdrFJe"):
            found = re.search(r'"' + key + r'"\s*:\s*("(?:[^"\\]|\\.)*")', page)
            if found:
                match _json(found.group(1)):
                    case str() as value:
                        values[key] = value
                    case int() | float() | list() | dict() | None:
                        raise AccountError("bootstrap_failed")
        if not values.get("SNlM0e") or not values.get("cfb2h"):
            raise AccountError("bootstrap_failed")
        self._xsrf, self._build = values["SNlM0e"], values["cfb2h"]
        self._session_id = values.get("FdrFJe")

    def rpc(self, rpcid: str, args: JsonValue, source_path: str = "/app") -> JsonValue:
        if self._xsrf is None:
            self.bootstrap()
        source = self._prefix + source_path
        params = {
            "rpcids": rpcid,
            "source-path": source,
            "bl": self._build,
            "hl": "en",
            "_reqid": str(self._request_id),
            "rt": "c",
        }
        if self._session_id is not None:
            params["f.sid"] = self._session_id
        self._request_id += 100000
        body = urlencode(
            {
                "f.req": json.dumps([[[rpcid, json.dumps(args), None, "generic"]]]),
                "at": self._xsrf,
            }
        ).encode()
        path = self._prefix + "/_/BardChatUi/data/batchexecute?" + urlencode(params)
        return _decode_rpc(self._request(path, body), rpcid)

    def account_models(self) -> AccountModels:
        body = _array(self.rpc("otAQ7b", []))
        if len(body) <= 14:
            raise AccountError("invalid_response")
        status = _field(body, 14)
        status_code = _integer(status)
        if status_code == 1016:
            raise AccountError("unauthenticated", provider_code=status_code)
        if status is not None and status_code != 1000:
            raise AccountError("account_unavailable", provider_code=status_code)
        models: list[ModelCapability] = []
        for row in _array(_field(body, 15)):
            capability_id, display = _field(row, 0), _field(row, 11)
            mode = _integer(_field(row, 17))
            match (capability_id, display):
                case (str(), str()) if capability_id and display and mode is not None:
                    models.append(ModelCapability(capability_id, display, mode))
                case _:
                    raise AccountError("invalid_response")
        flags = _field(body, 16)
        capacity_flags = (
            None if flags is None else tuple(_integer(flag) for flag in _array(flags))
        )
        return AccountModels(tuple(models), capacity_flags, status_code, time.time())

    def usage(self) -> AccountUsage:
        body = _array(self.rpc("jSf9Qc", [], "/usage"))
        if not body:
            raise AccountError("invalid_response")
        tier_code = _integer(_field(body, 0))
        overage_value = _field(body, 2)
        overage = overage_value if isinstance(overage_value, bool) else None
        rows = _field(body, 1)
        metrics: list[UsageMetric] = []
        if rows is not None:
            for row in _array(rows):
                metric_type = _integer(_field(_array(row), 2))
                windows: dict[int, WindowKind] = {1: "5h", 2: "weekly", 3: "ai_credit"}
                window = (
                    "unknown"
                    if metric_type is None
                    else windows.get(metric_type, "unknown")
                )
                metrics.append(
                    UsageMetric(
                        _number(_field(row, 0)),
                        _number(_field(row, 1)),
                        metric_type,
                        _number(_field(row, 3, 0, 0)),
                        window,
                    )
                )
        return AccountUsage(
            tier_code,
            "PRO" if tier_code == 2 else None,
            overage,
            None if rows is None else tuple(metrics),
            time.time(),
        )
