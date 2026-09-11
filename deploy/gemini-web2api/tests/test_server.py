from __future__ import annotations

import json
import threading
import unittest
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from http.client import HTTPConnection
from http.server import ThreadingHTTPServer
from unittest.mock import patch

from extension.account import (
    AccountError,
    AccountModels,
    AccountUsage,
    ModelCapability,
    UsageMetric,
)
from extension.credentials import encode_token
from extension.server import WebHandler


class FakeSession:
    def __init__(self, cookie: str, account_index: str) -> None:
        self.cookie = cookie
        self.account_index = account_index

    def usage(self) -> AccountUsage:
        if self.cookie == "SID=expired-account":
            raise AccountError("bootstrap_failed")
        remaining = 7 if self.cookie == "SID=account-a" else 9
        return AccountUsage(2, "PRO", False, (UsageMetric(remaining, 0.25, 1, 1800000000, "5h"),), 1700000000)

    def account_models(self) -> AccountModels:
        return AccountModels((ModelCapability("test-model-id", "3.8 Flash", 1),), (8,), None, 1700000000)

    def generate_flash(self, prompt: str) -> str:
        return "SYNTHETIC_RESPONSE"


class ServerTests(unittest.TestCase):
    def test_accounts_are_request_scoped_and_invalid_tokens_do_not_fallback(self) -> None:
        with patch("extension.server.NativeSession", FakeSession), ThreadingHTTPServer(("127.0.0.1", 0), WebHandler) as server:
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                def query(cookie: str, index: int) -> int:
                    with closing(HTTPConnection("127.0.0.1", server.server_port)) as client:
                        client.request("GET", "/v1/usage", headers={"x-goog-api-key": encode_token(cookie, index)})
                        response = client.getresponse()
                        self.assertEqual(response.status, 200)
                        body = json.loads(response.read())
                        self.assertFalse(body["estimated"])
                        self.assertEqual(body["metrics"][0]["unit"], "provider_compute_unit")
                        self.assertEqual(body["metrics"][0]["usage_percent"], 25)
                        return body["metrics"][0]["remaining_units"]

                with ThreadPoolExecutor(max_workers=2) as pool:
                    first = pool.submit(query, "SID=account-a", 0)
                    second = pool.submit(query, "SID=account-b", 2)
                    self.assertEqual((first.result(), second.result()), (7, 9))
                for path in ("/v1/usage", "/v1beta/models/gemini-web-omni:generateContent"):
                    with closing(HTTPConnection("127.0.0.1", server.server_port)) as client:
                        method = "POST" if ":" in path else "GET"
                        client.request(method, path, body="{}", headers={"x-goog-api-key": "gemini-web:v1:invalid"})
                        response = client.getresponse()
                        self.assertEqual(response.status, 400)
                        self.assertNotIn("invalid\"", response.read().decode())
                with closing(HTTPConnection("127.0.0.1", server.server_port)) as client:
                    client.request("GET", "/v1/usage", headers={"x-goog-api-key": encode_token("SID=expired-account")})
                    response = client.getresponse()
                    self.assertEqual(response.status, 502)
                    self.assertEqual(json.loads(response.read())["error"]["message"], "bootstrap_failed")
                with closing(HTTPConnection("127.0.0.1", server.server_port)) as client:
                    client.request("POST", "/v1/chat/completions", json.dumps({"model":"gemini-web-omni"}), {"x-goog-api-key":encode_token("SID=account-a")})
                    response = client.getresponse()
                    self.assertEqual(response.status, 400)
                    response.read()
            finally:
                server.shutdown()
                thread.join()
