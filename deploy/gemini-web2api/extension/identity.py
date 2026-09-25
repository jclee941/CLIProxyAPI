"""HTTP bootstrap identity proof and explicit Google sign-in markers."""

from __future__ import annotations

import hashlib
import re
from collections.abc import Callable
from html.parser import HTMLParser
from typing import override
from urllib.parse import urlsplit

from .credentials import _JSON_DECODER, CredentialError, _JsonValue


def account_digest(
    page: str,
    decode: Callable[[str], tuple[_JsonValue, int]] = _JSON_DECODER.raw_decode,
) -> str | None:
    assignments = list(re.finditer(r"\bWIZ_global_data\s*=\s*", page))
    if len(assignments) != 1:
        return None
    try:
        values, _ = decode(page[assignments[0].end():])
    except (ValueError, RecursionError, CredentialError):
        return None
    match values:
        case {"S06Grb": str() as first, "W3Yyqf": str() as second, "qDCSke": str() as third}:
            if first == second == third and re.fullmatch(r"[0-9]{21}", first):
                return hashlib.sha256(first.encode("utf-8")).hexdigest()
    return None


def is_signin_url(url: str) -> bool:
    try:
        target = urlsplit(url)
    except ValueError:
        return False
    return (
        target.scheme == "https"
        and target.hostname == "accounts.google.com"
        and target.username is None
        and target.password is None
        and (
            target.path in {"/ServiceLogin", "/ServiceLoginAuth"}
            or target.path.startswith(("/v3/signin/", "/signin/", "/v2/signin/"))
        )
    )


class _SigninPage(HTMLParser):
    """Accumulate structural login markers, never arbitrary text or script data."""

    def __init__(self) -> None:
        super().__init__()
        self.found: bool = False

    @override
    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        match tag:
            case "a":
                self.found |= is_signin_url(values.get("href") or "")
            case "form":
                self.found |= is_signin_url(values.get("action") or "") or (
                    values.get("id") == "gaia_loginform"
                    and values.get("action") == "/ServiceLoginAuth"
                )


def has_signin_marker(page: str) -> bool:
    parser = _SigninPage()
    parser.feed(page)
    return parser.found
