"""Synthetic wire tests: from the sidecar, python -m unittest discover -s tests -p test_account.py."""

from __future__ import annotations

import hashlib
import json
import threading
import unittest
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import asdict
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import override
from urllib.parse import parse_qs, urlsplit

import pytest

from extension.account import AccountError, HttpSession, JsonValue


def model_row(name: str, capability_id: str, mode: int) -> list[JsonValue]:
    row: list[JsonValue] = [None] * 18
    row[0], row[11], row[17] = capability_id, name, mode
    return row


def account_body(status: JsonValue = None) -> list[JsonValue]:
    body: list[JsonValue] = [None] * 17
    body[0] = "excluded@example.invalid"
    body[14] = status
    body[15] = [
        model_row("3.8 Flash", "synthetic-flash", 1),
        model_row("3.1 Pro", "synthetic-pro", 3),
        model_row("3.5 Flash-Lite", "synthetic-lite", 6),
    ]
    body[16] = [1, None, 4]
    return body


def frame(rpcid: str, body: JsonValue, rejection: JsonValue = None) -> bytes:
    payload = json.dumps(
        [["wrb.fr", rpcid, json.dumps(body), None, None, rejection]], ensure_ascii=False
    ).encode()
    return str(len(payload)).encode() + b"\n" + payload + b"\n"


class WireServer(ThreadingHTTPServer):
    """Owns mutable provider responses and captured synthetic requests."""

    def __init__(self) -> None:
        super().__init__(("127.0.0.1", 0), WireHandler)
        self.html: str = '<script>window.WIZ_global_data={"SNlM0e":"fake-xsrf", "cfb2h":"fake-build", "FdrFJe":"fake-session"};</script>'
        self.payload: bytes = b")]}'\n\n" + frame("otAQ7b", account_body())
        self.http_status: int = 200
        self.cookie: str = "SID=synthetic-only"
        self.requests: list[tuple[str, dict[str, str], bytes]] = []


class WireHandler(BaseHTTPRequestHandler):
    @property
    def wire_server(self) -> WireServer:
        assert isinstance(self.server, WireServer)
        return self.server

    def do_GET(self) -> None:
        self.respond(self.wire_server.html.encode())

    def do_POST(self) -> None:
        self.respond(self.wire_server.payload)

    def respond(self, payload: bytes) -> None:
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.wire_server.requests.append((self.path, dict(self.headers.items()), body))
        status = (
            self.wire_server.http_status
            if self.headers.get("Cookie") == self.wire_server.cookie
            else 401
        )
        self.send_response(status)
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("Location", "http://example.invalid/do-not-follow")
        self.end_headers()
        _ = self.wfile.write(payload)

    @override
    def log_message(self, format: str, *args: str | int) -> None:
        return


@contextmanager
def provider(
    account_index: str | None = None,
    *,
    cookie: str = "SID=synthetic-only",
) -> Iterator[tuple[WireServer, HttpSession]]:
    with WireServer() as server:
        server.cookie = cookie
        thread = threading.Thread(
            target=server.serve_forever, kwargs={"poll_interval": 0.01}
        )
        thread.start()
        try:
            yield (
                server,
                HttpSession(
                    cookie,
                    account_index,
                    upstream_url=f"http://127.0.0.1:{server.server_port}",
                ),
            )
        finally:
            server.shutdown()
            thread.join()


class AccountTests(unittest.TestCase):
    def test_google_frame_length_counts_utf16_units_for_unicode(self) -> None:
        with provider() as (server, session):
            body = account_body()
            models = body[15]
            assert isinstance(models, list)
            model = models[0]
            assert isinstance(model, list)
            model[11] = "3.8 Flash \ud55c\uae00 \U0001f680"
            payload = json.dumps([["wrb.fr", "otAQ7b", json.dumps(body, ensure_ascii=False)]], ensure_ascii=False)
            units = len(payload.encode("utf-16-le")) // 2
            server.payload = str(units + 2).encode() + b"\n" + payload.encode() + b"\n"
            self.assertEqual(session.account_models().models[0].display_name, "3.8 Flash \ud55c\uae00 \U0001f680")

    def test_google_frame_length_includes_delimiters_not_next_prefix(self) -> None:
        with provider() as (server, session):
            encoded = frame("otAQ7b", account_body())
            length, _, payload = encoded.partition(b"\n")
            server.payload = str(int(length) + 2).encode() + b"\n" + payload + b"4\n[]\n"
            self.assertTrue(session.account_models().available)

    def test_default_account_zero_uses_canonical_app_path(self) -> None:
        with provider("0") as (server, session):
            session.account_models()
            self.assertEqual(server.requests[0][0], "/app")

    def test_models_when_authenticated_use_exact_provider_capabilities(self) -> None:
        with provider() as (server, session):
            result = session.account_models()
            self.assertEqual(
                result.select_model("3.8 Flash").capability_id, "synthetic-flash"
            )
            self.assertEqual(result.select_model("3.8 Flash").mode, 1)
            self.assertEqual(result.capacity_flags, (1, None, 4))
            self.assertTrue(result.available)
            self.assertEqual(result.source, "GoogleWeb")
            self.assertFalse(result.estimated)
            self.assertGreater(result.observed_at, 0)
            self.assertNotIn("excluded@example.invalid", json.dumps(asdict(result)))
            self.assertEqual(len(server.requests), 2)

    def test_request_when_indexed_uses_bootstrap_and_batch_wire_contract(self) -> None:
        with provider("2") as (server, session):
            _ = session.account_models()
            self.assertEqual(server.requests[0][0], "/u/2/app")
            path, headers, data = server.requests[1]
            target = urlsplit(path)
            self.assertEqual(target.path, "/u/2/_/BardChatUi/data/batchexecute")
            query = parse_qs(target.query)
            for key, value in {
                "rpcids": "otAQ7b",
                "source-path": "/u/2/app",
                "bl": "fake-build",
                "hl": "en",
                "rt": "c",
                "f.sid": "fake-session",
            }.items():
                self.assertEqual(query[key], [value])
            self.assertTrue(query["_reqid"][0].isdigit())
            self.assertEqual(headers["Cookie"], "SID=synthetic-only")
            self.assertEqual(headers["Referer"], headers["Origin"] + "/u/2/app")
            self.assertEqual(
                headers["Content-Type"],
                "application/x-www-form-urlencoded;charset=UTF-8",
            )
            self.assertEqual(headers["x-goog-ext-73010989-jspb"], "[0]")
            self.assertEqual(
                json.loads(headers["x-goog-ext-525001261-jspb"]),
                [1] + [None] * 7 + [[4, 5, 6, 8]] + [None] * 7,
            )
            form = parse_qs(data.decode())
            self.assertEqual(form["at"], ["fake-xsrf"])
            self.assertEqual(
                json.loads(form["f.req"][0]), [[["otAQ7b", "[]", None, "generic"]]]
            )

    def test_models_when_status_1016_fail_unauthenticated(self) -> None:
        with provider() as (server, session):
            server.payload = b")]}'\n" + frame("otAQ7b", account_body(1016))
            with self.assertRaises(AccountError) as raised:
                _ = session.account_models()
            self.assertEqual(
                (raised.exception.kind, raised.exception.provider_code),
                ("unauthenticated", 1016),
            )

    def test_rpc_when_denied_rejects_even_with_success_body_earlier(self) -> None:
        with provider() as (server, session):
            server.payload += frame("otAQ7b", account_body(), [7])
            with self.assertRaises(AccountError) as raised:
                _ = session.account_models()
            self.assertEqual(
                (raised.exception.kind, raised.exception.provider_code),
                ("rpc_denied", 7),
            )

    def test_model_when_only_other_version_exists_has_no_fallback(self) -> None:
        with provider() as (server, session):
            body = account_body(1000)
            body[15] = [model_row("3.7 Flash", "synthetic-old", 1)]
            server.payload = frame("otAQ7b", body)
            result = session.account_models()
            with self.assertRaises(AccountError) as raised:
                _ = result.select_model("3.8 Flash")
            self.assertEqual(raised.exception.kind, "model_unavailable")

    def test_usage_when_provider_reports_compute_units_preserves_values(self) -> None:
        with provider() as (server, session):
            server.payload = (
                b")]}'\n\n"
                + frame("other", ["ignore"])
                + frame(
                    "jSf9Qc",
                    [
                        2,
                        [
                            [43210, 0.0123456789, 2, [[1900000000]]],
                            [1234, 0.23456789, 1, [[1800000000]]],
                            [77, None, 3, None],
                            [None, None, 99, None],
                        ],
                        False,
                    ],
                )
            )
            result = session.usage()
            assert result.metrics is not None
            self.assertEqual(
                (result.tier_code, result.tier, result.overage), (2, "PRO", False)
            )
            self.assertEqual(
                [row.window_kind for row in result.metrics],
                ["weekly", "5h", "ai_credit", "unknown"],
            )
            weekly, rolling, credits, unknown = result.metrics
            self.assertEqual(
                (weekly.remaining_units, weekly.usage_fraction, weekly.reset_at),
                (43210, 0.0123456789, 1900000000),
            )
            self.assertEqual(rolling.remaining_units, 1234)
            self.assertEqual(rolling.usage_percent, 0.23456789 * 100)
            self.assertEqual(credits.unit, "provider_compute_unit")
            self.assertIsNone(unknown.remaining_units)
            self.assertIsNone(unknown.usage_percent)
            self.assertEqual(result.source, "GoogleWeb")
            self.assertFalse(result.estimated)
            self.assertGreater(result.observed_at, 0)
            self.assertEqual(
                parse_qs(urlsplit(server.requests[1][0]).query)["source-path"],
                ["/usage"],
            )

    def test_bootstrap_when_required_data_missing_fails_without_rpc(self) -> None:
        with provider() as (server, session):
            server.html = '<script>{"SNlM0e":"fake-xsrf"}</script>'
            with self.assertRaises(AccountError) as raised:
                _ = session.account_models()
            self.assertEqual(raised.exception.kind, "bootstrap_failed")
            self.assertEqual(len(server.requests), 1)

    def test_http_when_error_or_redirect_is_redacted_and_not_retried(self) -> None:
        for status in (302, 401, 500):
            with self.subTest(status=status), provider() as (server, session):
                server.http_status = status
                with self.assertRaises(AccountError) as raised:
                    session.bootstrap()
                self.assertEqual(raised.exception.http_status, status)
                self.assertEqual(len(server.requests), 1)
                self.assertNotIn("synthetic-only", str(raised.exception))
                self.assertNotIn("http", str(raised.exception))
                self.assertNotIn("fake-xsrf", repr(raised.exception))

    def test_rpc_when_frame_truncated_or_missing_is_protocol_error(self) -> None:
        for payload in (b"100\n[]\n", b"2\n{}\n", frame("other", []), b"not-json"):
            with self.subTest(payload=payload), provider() as (server, session):
                server.payload = payload
                with self.assertRaises(AccountError) as raised:
                    _ = session.account_models()
                self.assertEqual(raised.exception.kind, "invalid_response")

    def test_config_when_origin_is_malformed_returns_safe_typed_error(self) -> None:
        for origin in (
            "http://[synthetic-only",
            "http://localhost:invalid",
            "https://user:synthetic-only@localhost",
        ):
            with self.subTest(origin=origin):
                with self.assertRaises(AccountError) as raised:
                    _ = HttpSession("SID=synthetic-only", upstream_url=origin)
                self.assertEqual(raised.exception.kind, "invalid_config")
                self.assertNotIn("synthetic-only", str(raised.exception))

    def test_usage_when_fields_are_unknown_does_not_fabricate_zero(self) -> None:
        with provider() as (server, session):
            server.payload = frame(
                "jSf9Qc", [99, [[True, "unknown", True, [[False]]]], 1]
            )
            result = session.usage()
            self.assertEqual(result.tier_code, 99)
            self.assertIsNone(result.tier)
            self.assertIsNone(result.overage)
            assert result.metrics is not None
            self.assertEqual(
                asdict(result.metrics[0]),
                {
                    "remaining_units": None,
                    "usage_fraction": None,
                    "metric_type": None,
                    "reset_at": None,
                    "window_kind": "unknown",
                    "unit": "provider_compute_unit",
                },
            )

    def test_usage_when_metrics_missing_distinguishes_empty_list(self) -> None:
        cases: tuple[JsonValue, ...] = (None, [])
        for rows in cases:
            with self.subTest(rows=rows), provider() as (server, session):
                server.payload = frame("jSf9Qc", [2, rows, False])
                result = session.usage()
                self.assertEqual(result.metrics, None if rows is None else ())

    def test_rpc_when_utf8_frames_and_optional_session_absent_reuses_bootstrap(
        self,
    ) -> None:
        with provider() as (server, session):
            server.html = (
                '<script>{"SNlM0e":"fake\\u002dtoken", "cfb2h":"fake-build"}</script>'
            )
            server.payload = frame("other", ["\u2603"]) + frame(
                "otAQ7b", account_body()
            )
            session.bootstrap()
            _ = session.account_models()
            _ = session.account_models()
            self.assertEqual(len(server.requests), 3)
            first_query = parse_qs(urlsplit(server.requests[1][0]).query)
            last_query = parse_qs(urlsplit(server.requests[2][0]).query)
            self.assertNotIn("f.sid", first_query)
            self.assertNotEqual(first_query["_reqid"], last_query["_reqid"])
            self.assertEqual(
                parse_qs(server.requests[1][2].decode())["at"], ["fake-token"]
            )

    def test_models_when_status_unknown_or_shape_missing_is_not_available(self) -> None:
        for body in (account_body(1999), account_body(False), []):
            with self.subTest(body=body), provider() as (server, session):
                server.payload = frame("otAQ7b", body)
                with self.assertRaises(AccountError):
                    _ = session.account_models()


@pytest.mark.parametrize("account_index", [None, "0", "2"])
@pytest.mark.parametrize("cookie", [
    "SAPISID=synthetic-sapi",
    "SID=synthetic-only; SAPISID=synthetic-sapi; OTHER=retained=value",
    "OTHER=retained;SAPISID=synthetic-sapi;SID=synthetic-only",
])
def test_rpc_signs_each_request_when_sapisid_exists(
    account_index: str | None, cookie: str, monkeypatch: pytest.MonkeyPatch,
) -> None:
    with provider(account_index, cookie=cookie) as (server, session):
        origin = f"http://127.0.0.1:{server.server_port}"
        monkeypatch.setattr("extension.account.time.time", lambda: 1700000000.75 + len(server.requests))

        _ = session.rpc("otAQ7b", [])
        _ = session.rpc("otAQ7b", [])

        assert len(server.requests) == 3
        for index, (path, headers, _) in enumerate(server.requests):
            timestamp = 1700000000 + index
            digest = hashlib.sha1(f"{timestamp} synthetic-sapi {origin}".encode()).hexdigest()
            assert headers["Authorization"] == f"SAPISIDHASH {timestamp}_{digest}"
            assert headers["Cookie"] == cookie
            assert headers["Origin"] == origin
            assert headers["X-Same-Domain"] == "1"
            assert headers.get("User-Agent") == "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", "Preserve the known-working source bridge HTTP identity"
            prefix = "" if account_index in (None, "0") else f"/u/{account_index}"
            assert path.startswith(prefix + "/")
            assert headers["Referer"] == origin + prefix + "/app"
            assert headers.get("X-Goog-AuthUser") == (account_index if prefix else None)


@pytest.mark.parametrize("cookie", [
    "SID=synthetic-only", "__Secure-3PAPISID=other; SID=synthetic-only",
    "SAPISID=; OTHER=preserved", "XSAPISID=other; sapisid=lowercase",
])
def test_rpc_preserves_cookies_when_sapisid_absent(cookie: str) -> None:
    with provider(cookie=cookie) as (server, session):
        _ = session.rpc("otAQ7b", [])

        assert len(server.requests) == 2
        for _, headers, _ in server.requests:
            assert "Authorization" not in headers
            assert headers["Cookie"] == cookie


@pytest.mark.parametrize("status", [302, 403])
def test_signed_http_error_keeps_credentials_private(
    status: int, caplog: pytest.LogCaptureFixture, capsys: pytest.CaptureFixture[str],
) -> None:
    cookie = "SID=synthetic-only; SAPISID=synthetic-sapi"
    with provider(cookie=cookie) as (server, session):
        server.http_status = status

        with pytest.raises(AccountError) as raised:
            session.bootstrap()

        assert raised.value.http_status == status
        assert len(server.requests) == 1
        authorization = server.requests[0][1]["Authorization"]
        captured = capsys.readouterr()
        exposed = str(raised.value) + repr(raised.value) + caplog.text + captured.out + captured.err
        assert cookie not in exposed
        assert "synthetic-sapi" not in exposed
        assert authorization not in exposed
        assert authorization.split("_", 1)[1] not in exposed


if __name__ == "__main__":
    _ = unittest.main()
