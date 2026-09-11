from __future__ import annotations

import json
import threading
from collections.abc import Iterator
from contextlib import closing, contextmanager
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import override
from unittest.mock import patch

import pytest

from extension.account import AccountError
from extension.credentials import SessionCredential, decode_token, encode_token
from extension.native import NativeSession
from extension.renewal import renew_session
from extension.server import WebHandler


class RenewalWire(ThreadingHTTPServer):
    def __init__(self) -> None:
        super().__init__(("127.0.0.1", 0), RenewalHandler)
        self.status: int = 200
        self.account_status: int = 1000
        self.cookies: list[str] = []
        self.requests: list[tuple[str, str, dict[str, str], bytes]] = []


class RenewalHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        self.do_POST()

    def do_POST(self) -> None:
        assert isinstance(self.server, RenewalWire)
        server = self.server
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        server.requests.append((self.command, self.path, dict(self.headers), body))
        if self.path == "/RotateCookies":
            self.send_response(server.status)
            for cookie in server.cookies:
                self.send_header("Set-Cookie", cookie)
            self.send_header("Location", "https://example.invalid/never-follow")
            payload = b"synthetic-private-upstream-body"
        else:
            self.send_response(200)
            account = json.dumps([None] * 14 + [server.account_status, [], []])
            frame = json.dumps([["wrb.fr", "otAQ7b", account, None, None, None]]).encode()
            payload = (
                b'{"SNlM0e":"synthetic-xsrf","cfb2h":"synthetic-build"}'
                if self.command == "GET"
                else b")]}'\n\n" + str(len(frame)).encode() + b"\n" + frame + b"\n"
            )
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        _ = self.wfile.write(payload)

    @override
    def log_message(self, format: str, *args: str | int) -> None:
        return


@contextmanager
def renewal_wire() -> Iterator[RenewalWire]:
    with RenewalWire() as server:
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.start()

        def connection(host: str, *, timeout: None) -> HTTPConnection:
            assert host == "accounts.google.com"
            assert timeout is None
            return HTTPConnection("127.0.0.1", server.server_port, timeout=None)

        def session(cookie: str, account_index: str) -> NativeSession:
            return NativeSession(cookie, account_index, upstream_url=f"http://127.0.0.1:{server.server_port}")

        try:
            with patch("extension.renewal.HTTPSConnection", connection), patch("extension.renewal.NativeSession", session):
                yield server
        finally:
            server.shutdown()
            thread.join()


def test_renewal_retains_pairs_and_auth_user_when_google_rotates() -> None:
    original = "SID=synthetic-sid; SAPISID=synthetic-sapi; __Secure-3PSIDTS=old; NID=keep=equals"
    with renewal_wire() as server:
        server.cookies = [
            "__Secure-3PSIDTS=fresh; Domain=.google.com; Path=/; Secure; HttpOnly; Priority=High",
            "SIDCC=fresh-cc; Domain=.google.com; Path=/; Secure; SameSite=None",
            "__Secure-1PSIDCC=fresh-1cc; Domain=.google.com; Path=/; Secure",
            "__Secure-3PSIDCC=fresh-3cc; Domain=.google.com; Path=/; Secure",
        ]

        token = renew_session(SessionCredential(original, 2))

        renewed = decode_token(token)
        assert renewed.auth_user == 2
        assert renewed.cookie == original.replace("3PSIDTS=old", "3PSIDTS=fresh") + "; SIDCC=fresh-cc; __Secure-1PSIDCC=fresh-1cc; __Secure-3PSIDCC=fresh-3cc"
        method, path, headers, body = server.requests[0]
        assert (method, path, body) == ("POST", "/RotateCookies", b'[000,"-0000000000000000000"]')
        assert headers["Cookie"] == original
        assert headers["Origin"] == "https://accounts.google.com"
        assert headers["Content-Type"] == "application/json"
        assert "Chrome/150." in headers["User-Agent"]
        assert len(server.requests) == 3
        assert server.requests[1][1] == "/u/2/app"
        assert all(request[2]["Cookie"] == renewed.cookie for request in server.requests[1:])


@pytest.mark.parametrize("header", [
    "SID=poison; Domain=evil.google.com; Path=/; Secure",
    "SID=poison; Domain=.google.com.evil.invalid; Path=/; Secure",
    "SID=poison; Domain=..google.com; Path=/; Secure",
    "SID=poison; Path=/; Secure",
    "SID=poison; Domain=.google.com; Path=/accounts; Secure",
    "SID=poison; Domain=.google.com; Secure",
    "SID=poison; Domain=.google.com; Path=/",
    "Priority=High; Domain=.google.com; Path=/; Secure",
    "priority=High; Domain=.google.com; Path=/; Secure",
])
def test_renewal_ignores_cookies_outside_gemini_contract(header: str) -> None:
    with renewal_wire() as server:
        server.cookies = [header, "SIDCC=fresh; Priority=High; Domain=.gemini.google.com; Path=/; Secure"]

        token = renew_session(SessionCredential("SID=synthetic-original", 0))

        assert decode_token(token).cookie == "SID=synthetic-original; SIDCC=fresh"


@pytest.mark.parametrize("status", [401, 302, 500])
def test_renewal_never_returns_token_when_rotation_fails(status: int) -> None:
    with renewal_wire() as server:
        server.status = status

        with pytest.raises(AccountError) as caught:
            renew_session(SessionCredential("SID=synthetic-original", 0))

        assert caught.value.http_status == status
        assert "synthetic" not in str(caught.value)
        assert len(server.requests) == 1


def test_renewal_never_returns_token_when_new_session_is_unavailable() -> None:
    with renewal_wire() as server:
        server.account_status = 1016

        with pytest.raises(AccountError) as caught:
            renew_session(SessionCredential("SID=synthetic-original", 0))

        assert caught.value.kind == "unauthenticated"


@pytest.mark.parametrize("key,body,status", [
    (encode_token("SID=synthetic-original", 2), b"", 200),
    ("synthetic-legacy-key", b"", 401),
    ("", b"", 401),
    ("gemini-web:v1:invalid", b"", 400),
    (encode_token("SID=synthetic-original"), b"{}", 400),
])
def test_private_renewal_http_contract(key: str, body: bytes, status: int, capsys: pytest.CaptureFixture[str]) -> None:
    with renewal_wire() as upstream, patch("extension.server.CONFIG", {"api_keys": ["synthetic-legacy-key"]}), patch("extension.server.load_cookie") as legacy:
        upstream.cookies = ["SIDCC=fresh; Domain=.google.com; Path=/; Secure"]
        with ThreadingHTTPServer(("127.0.0.1", 0), WebHandler) as server:
            thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
            thread.start()
            try:
                with closing(HTTPConnection("127.0.0.1", server.server_port)) as client:
                    client.request("POST", "/v1/session/renew", body, {"x-goog-api-key": key})
                    with client.getresponse() as response:
                        payload = response.read()
                        assert response.status == status
                        assert response.getheader("Cache-Control") == "no-store"
                if status == 200:
                    assert payload == json.dumps({"token": encode_token("SID=synthetic-original; SIDCC=fresh", 2)}).encode()
                    assert len(upstream.requests) == 3
                else:
                    assert b"token" not in payload and b"synthetic" not in payload
                    assert upstream.requests == []
                legacy.assert_not_called()
                captured = capsys.readouterr()
                assert "synthetic" not in captured.out + captured.err
            finally:
                server.shutdown()
                thread.join()


@pytest.mark.parametrize("provider_status,account_status", [(401, 1000), (200, 1016)])
def test_private_renewal_returns_safe_http_error_when_google_rejects(provider_status: int, account_status: int) -> None:
    with renewal_wire() as upstream, ThreadingHTTPServer(("127.0.0.1", 0), WebHandler) as server:
        upstream.status = provider_status
        upstream.account_status = account_status
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.start()
        try:
            with closing(HTTPConnection("127.0.0.1", server.server_port)) as client:
                client.request("POST", "/v1/session/renew", headers={"x-goog-api-key": encode_token("SID=synthetic-original")})
                with client.getresponse() as response:
                    payload = response.read()
                    assert response.status == 401
                    assert response.getheader("Cache-Control") == "no-store"
                    assert b"token" not in payload and b"synthetic" not in payload
        finally:
            server.shutdown()
            thread.join()


def test_renewal_sanitizes_transport_errors_without_fallback() -> None:
    with patch("extension.renewal.HTTPSConnection", side_effect=OSError("synthetic-private-transport")) as connection:
        with pytest.raises(AccountError) as caught:
            renew_session(SessionCredential("SID=synthetic-original", 0))

        assert caught.value.kind == "transport_failed"
        assert "synthetic" not in str(caught.value)
        assert connection.call_count == 1
