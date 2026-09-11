"""Pure credential transport, not encryption, signing, or Google authentication.

Supply the complete Cookie header value. Only header transport safety is checked;
Google determines whether that cookie set authenticates the selected account.
"""

from __future__ import annotations

import base64
import binascii
import json
import re
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Final, Literal, override

TOKEN_PREFIX: Final = "gemini-web:v1:"
MAX_TOKEN_LENGTH: Final = 32768

type CredentialErrorKind = Literal[
    "missing_credential",
    "unknown_credential",
    "invalid_credential",
    "credential_too_long",
]
type _JsonValue = (
    str | int | float | bool | None | list[_JsonValue] | dict[str, _JsonValue]
)


@dataclass(frozen=True, slots=True)
class SessionCredential:
    cookie: str = field(repr=False)
    auth_user: int


class CredentialError(Exception):
    """Safe public error containing only a fixed category and HTTP status."""

    def __init__(self, kind: CredentialErrorKind, status: int) -> None:
        self.kind: CredentialErrorKind = kind
        self.status: int = status
        super().__init__(kind, status)

    @override
    def __str__(self) -> str:
        return f"{self.kind} (status={self.status})"


def _unique_fields(pairs: list[tuple[str, _JsonValue]]) -> _JsonValue:
    values: dict[str, _JsonValue] = {}
    for key, value in pairs:
        if key in values:
            raise CredentialError("invalid_credential", 400)
        values[key] = value
    return values


_JSON_DECODER: Final = json.JSONDecoder(object_pairs_hook=_unique_fields)


def _decode_json(
    payload: bytes, decode: Callable[[str], _JsonValue] = _JSON_DECODER.decode
) -> _JsonValue:
    return decode(payload.decode("utf-8"))


def _parse_session(cookie: _JsonValue, auth_user: _JsonValue) -> SessionCredential:
    match cookie:
        case str():
            if not cookie.strip() or any(not 32 <= ord(char) <= 126 for char in cookie):
                raise CredentialError("invalid_credential", 400)
        case int() | float() | list() | dict() | None:
            raise CredentialError("invalid_credential", 400)
    match auth_user:
        case bool():
            raise CredentialError("invalid_credential", 400)
        case int():
            if auth_user < 0:
                raise CredentialError("invalid_credential", 400)
        case str() | float() | list() | dict() | None:
            raise CredentialError("invalid_credential", 400)
    return SessionCredential(cookie, auth_user)


def encode_token(cookie: str, auth_user: int = 0) -> str:
    """Encode a complete cookie and index; cap the entire ASCII token at 32768."""
    if len(cookie) > MAX_TOKEN_LENGTH:
        raise CredentialError("credential_too_long", 431)
    credential = _parse_session(cookie, auth_user)
    try:
        payload = json.dumps(
            {"cookie": credential.cookie, "auth_user": credential.auth_user},
            separators=(",", ":"),
            ensure_ascii=False,
        ).encode("utf-8")
    except ValueError:
        raise CredentialError("invalid_credential", 400) from None
    token = TOKEN_PREFIX + base64.urlsafe_b64encode(payload).decode("ascii").rstrip("=")
    if len(token) > MAX_TOKEN_LENGTH:
        raise CredentialError("credential_too_long", 431)
    return token


def decode_token(token: str) -> SessionCredential:
    """Decode only v1, unpadded canonical base64url, UTF-8, and the exact schema."""
    if len(token) > MAX_TOKEN_LENGTH:
        raise CredentialError("credential_too_long", 431)
    if not token:
        raise CredentialError("missing_credential", 401)
    if not token.startswith("gemini-web:"):
        raise CredentialError("unknown_credential", 401)
    if not token.startswith(TOKEN_PREFIX):
        raise CredentialError("invalid_credential", 400)
    encoded = token[len(TOKEN_PREFIX) :]
    if re.fullmatch(r"[A-Za-z0-9_-]+", encoded) is None:
        raise CredentialError("invalid_credential", 400)
    try:
        payload = base64.b64decode(
            encoded + "=" * (-len(encoded) % 4), altchars=b"-_", validate=True
        )
        if base64.urlsafe_b64encode(payload).decode("ascii").rstrip("=") != encoded:
            raise CredentialError("invalid_credential", 400)
        value = _decode_json(payload)
    except (binascii.Error, ValueError, RecursionError):
        raise CredentialError("invalid_credential", 400) from None
    match value:
        case dict():
            if value.keys() != {"cookie", "auth_user"}:
                raise CredentialError("invalid_credential", 400)
            return _parse_session(value["cookie"], value["auth_user"])
        case str() | int() | float() | list() | None:
            raise CredentialError("invalid_credential", 400)


def resolve_credential(
    presented: str | None,
    legacy_lookup: Callable[[str], SessionCredential | None],
) -> SessionCredential:
    """Never call legacy_lookup for web tokens, even malformed or unsupported ones.

    The callback must use constant-time exact legacy-key matching, return None
    for unknown keys, and only load legacy cookies after a successful match.
    """
    if presented is None or not presented:
        raise CredentialError("missing_credential", 401)
    if len(presented) > MAX_TOKEN_LENGTH:
        raise CredentialError("credential_too_long", 431)
    if presented.startswith("gemini-web:"):
        return decode_token(presented)
    credential = legacy_lookup(presented)
    if credential is None:
        raise CredentialError("unknown_credential", 401)
    return credential
