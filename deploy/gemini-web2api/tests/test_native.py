from __future__ import annotations

import hashlib
import json
import threading
import unittest
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import override
from unittest.mock import patch
from urllib.parse import parse_qs

import pytest

from extension.account import AccountError, JsonValue
from extension.native import NativeSession


def frame(rpc: str, value: JsonValue) -> bytes:
    body = json.dumps([["wrb.fr", rpc, json.dumps(value)]]).encode()
    return b")]}'\n\n" + str(len(body)).encode() + b"\n" + body + b"\n"


@pytest.mark.parametrize("account_index", ["0", "3"])
@pytest.mark.parametrize("cookie", [
    "SID=synthetic-only; SAPISID=synthetic-sapi; OTHER=kept=value",
    "SID=synthetic-only; __Secure-3PAPISID=other",
])
def test_generation_and_rpc_share_fresh_authentication(
    account_index: str, cookie: str, monkeypatch: pytest.MonkeyPatch,
) -> None:
    observed: list[tuple[str, dict[str, str]]] = []
    row: list[JsonValue] = [None] * 18
    row[0], row[11], row[17] = "exact-account-model-id", "3.8 Flash", 1
    models: list[JsonValue] = [None] * 17
    models[15], models[16] = [row], [8]

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            self.respond(b'{"SNlM0e":"test-xsrf","cfb2h":"test-build"}')

        def do_POST(self) -> None:
            _ = self.rfile.read(int(self.headers["Content-Length"]))
            body = frame("otAQ7b", models) if "batchexecute" in self.path else frame(
                "generate", [None, None, None, None, [[None, ["FLASH_TEST_OK"]]]],
            )
            self.respond(body)

        def respond(self, body: bytes) -> None:
            observed.append((self.path, dict(self.headers.items())))
            self.send_response(200)
            self.end_headers()
            _ = self.wfile.write(body)

        @override
        def log_message(self, format: str, *args: str | int) -> None:
            return

    with ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.start()
        try:
            def connect(host: str, *, timeout: None) -> HTTPConnection:
                assert host == "gemini.google.com"
                assert timeout is None
                return HTTPConnection("127.0.0.1", server.server_port, timeout=timeout)

            monkeypatch.setattr("extension.account.HTTPSConnection", connect)
            monkeypatch.setattr("extension.native.HTTPSConnection", connect)
            monkeypatch.setattr("extension.account.time.time", lambda: 1700000000.75 + len(observed))
            session = NativeSession(cookie, account_index)

            assert session.generate_flash("short reply") == "FLASH_TEST_OK"
            assert session.submit_video("short clip")

            assert len(observed) == 5
            for index, (path, headers) in enumerate(observed):
                timestamp = 1700000000 + index
                digest = hashlib.sha1(f"{timestamp} synthetic-sapi https://gemini.google.com".encode()).hexdigest()
                expected_auth = f"SAPISIDHASH {timestamp}_{digest}" if "SAPISID=" in cookie else None
                assert headers.get("Authorization") == expected_auth
                assert headers["Cookie"] == cookie
                assert headers["Origin"] == "https://gemini.google.com"
                assert headers["X-Same-Domain"] == "1"
                prefix = "" if account_index == "0" else f"/u/{account_index}"
                assert path.startswith(prefix + "/")
                assert headers["Referer"] == "https://gemini.google.com" + prefix + "/app"
                assert headers.get("X-Goog-AuthUser") == (account_index if prefix else None)
                assert headers["Content-Type"] == "application/x-www-form-urlencoded;charset=UTF-8"
                assert headers["x-goog-ext-73010989-jspb"] == "[0]"
                if "StreamGenerate" in path:
                    assert json.loads(headers["x-goog-ext-525001261-jspb"]) == [1, None, None, None, "exact-account-model-id", None, None, 0, [4, 5, 6, 8], None, None, 2, None, None, 1]
                    assert headers["x-goog-ext-73010990-jspb"] == "[0,0,0]"
        finally:
            server.shutdown()
            thread.join()


class GenerationTests(unittest.TestCase):
    def test_generated_video_download_uses_the_observed_google_origin(self) -> None:
        observed: list[str | None] = []
        video = b"\x00\x00\x00\x18ftypmp42verified-test-video"

        class DownloadHandler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:
                observed.append(self.headers.get("Cookie"))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(video)

            def log_message(self, format: str, *args: str | int) -> None:
                return

        with ThreadingHTTPServer(("127.0.0.1", 0), DownloadHandler) as server:
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                def connect(host: str, *, timeout: None = None) -> HTTPConnection:
                    self.assertEqual(host, "contribution.usercontent.google.com")
                    return HTTPConnection("127.0.0.1", server.server_port, timeout=timeout)

                with patch("extension.native.HTTPSConnection", side_effect=connect):
                    result = NativeSession("SID=download-test").download_video("https://contribution.usercontent.google.com/generated-video")
                self.assertEqual(result, (200, video))
                self.assertEqual(observed, ["SID=download-test"])
            finally:
                server.shutdown()
                thread.join()

    def test_dynamic_flash_and_video_tool_use_cookie_http_only(self) -> None:
        submissions: list[tuple[list[JsonValue], str]] = []
        row: list[JsonValue] = [None] * 18
        row[0], row[11], row[17] = "exact-account-model-id", "3.8 Flash", 1
        models: list[JsonValue] = [None] * 17
        models[15], models[16] = [row], [8]

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:
                body = b'{"SNlM0e":"test-xsrf","cfb2h":"test-build"}'
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self) -> None:
                form = parse_qs(self.rfile.read(int(self.headers["Content-Length"])).decode())
                if "batchexecute" in self.path:
                    body = frame("otAQ7b", models)
                else:
                    fields: list[JsonValue] = json.loads(json.loads(form["f.req"][0])[1])
                    submissions.append((fields, self.headers["x-goog-ext-525001261-jspb"]))
                    candidate: list[JsonValue] = [None] * 13
                    candidate[1] = ["FLASH_TEST_OK"]
                    output: list[JsonValue] = [None] * 5
                    output[1], output[4] = ["c_test", "r_test"], [candidate]
                    body = frame("generate", output)
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, format: str, *args: str | int) -> None:
                return

        with ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                session = NativeSession("SID=test-cookie", upstream_url=f"http://127.0.0.1:{server.server_port}")
                self.assertEqual(session.generate_flash("short reply"), "FLASH_TEST_OK")
                session.submit_video("short clip")
                self.assertEqual(len(submissions), 2)
                self.assertEqual(json.loads(submissions[0][1])[4], "exact-account-model-id")
                video_fields = submissions[1][0]
                self.assertEqual(video_fields[55], [[16]])
                self.assertEqual(video_fields[49], 11)
                self.assertIsNone(video_fields[3])
                self.assertIsNone(video_fields[4])
                self.assertIsNone(video_fields[45])
                row[11] = "3.6 Flash"
                with self.assertRaises(AccountError):
                    session.generate_flash("not 3.8")
                self.assertEqual(len(submissions), 2)
            finally:
                server.shutdown()
                thread.join()
