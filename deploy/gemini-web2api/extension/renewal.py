"""One HTTP credential-acquisition step for an explicitly selected video session."""

from __future__ import annotations

from contextlib import closing
from http.client import HTTPException, HTTPSConnection
from http.cookies import CookieError, SimpleCookie
from typing import Final

from .account import AccountError
from .credentials import SessionCredential, encode_token
from .native import NativeSession

_USER_AGENT: Final = (
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
)


def renew_session(credential: SessionCredential) -> str:
    """Rotate only at Google's fixed origin, then bootstrap and verify anew.

    This does not recover expired credentials or retry a generation. Cookie
    scope must cover every Gemini RPC path, hence only secure root-path cookies
    are merged into the full header. No session state or token is stored here.
    """
    updates: dict[str, str] = {}
    try:
        with closing(HTTPSConnection("accounts.google.com", timeout=None)) as connection:
            connection.request(
                "POST",
                "/RotateCookies",
                b'[000,"-0000000000000000000"]',
                {
                    "Cookie": credential.cookie,
                    "Content-Type": "application/json",
                    "Origin": "https://accounts.google.com",
                    "User-Agent": _USER_AGENT,
                },
            )
            with connection.getresponse() as response:
                if response.status != 200:
                    raise AccountError("upstream_status", http_status=response.status)
                for header, value in response.getheaders():
                    if header.lower() != "set-cookie":
                        continue
                    parts = value.split(";")
                    name = parts[0].partition("=")[0].strip()
                    if name.lower() == "priority":
                        continue
                    cleaned = ";".join(
                        part for part in parts
                        if part.partition("=")[0].strip().lower() != "priority"
                    )
                    parsed = SimpleCookie()
                    parsed.load(cleaned)
                    morsel = parsed.get(name)
                    if morsel is None:
                        continue
                    match (morsel["domain"], morsel["path"], morsel["secure"]):
                        case (str() as domain, "/", True) if domain.lower().removeprefix(".") in {"google.com", "gemini.google.com"}:
                            updates[name] = morsel.coded_value
                        case _:
                            continue
    except (OSError, HTTPException, ValueError, CookieError):
        raise AccountError("transport_failed") from None

    pairs: list[str] = []
    present: set[str] = set()
    for pair in credential.cookie.split(";"):
        name = pair.strip().partition("=")[0]
        present.add(name)
        pairs.append(f"{name}={updates[name]}" if name in updates else pair.strip())
    pairs.extend(f"{name}={value}" for name, value in updates.items() if name not in present)
    cookie = "; ".join(pairs) if updates else credential.cookie
    token = encode_token(cookie, credential.auth_user)
    _ = NativeSession(cookie, str(credential.auth_user)).account_models()
    return token
