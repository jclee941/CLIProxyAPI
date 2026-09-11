"""Add authenticated native web generation and account usage to the pinned bridge."""

from __future__ import annotations

import base64
import hmac
import json
import re
import select
import socket
from dataclasses import asdict
from typing import TypedDict
from urllib.parse import urlsplit

from gemini_web2api.config import CONFIG
from gemini_web2api.gemini import load_cookie
from gemini_web2api.server import GeminiHandler
from gemini_web2api.tools import google_contents_to_prompt, parse_google_function_calls

from .account import AccountError
from .credential_worker import WorkerError, account_failure, run_credential_operation
from .credentials import (
    MAX_TOKEN_LENGTH,
    CredentialError,
    SessionCredential,
    _decode_json,
    decode_token,
    resolve_credential,
)
from .native import NativeSession
from .video import MODEL, VideoError, generate_video, parse_prompt

FLASH_MODEL = "gemini-3.8-flash"


class ModelRow(TypedDict):
    id: str
    description: str
    methods: list[str]


class WebHandler(GeminiHandler):
    def end_headers(self) -> None:
        if self.path in {"/v1/session/inspect", "/v1/session/renew"}:
            self.send_header("Cache-Control", "no-store")
        super().end_headers()

    def _presented(self) -> str | None:
        for name in ("x-goog-api-key", "x-api-key"):
            value = self.headers.get(name)
            if value:
                return value
        authorization = self.headers.get("Authorization", "")
        return authorization[7:] if authorization.startswith("Bearer ") else None

    def _legacy(self, key: str) -> SessionCredential | None:
        keys = CONFIG.get("api_keys")
        if not isinstance(keys, list) or not any(isinstance(value, str) and hmac.compare_digest(key.encode(), value.encode()) for value in keys):
            return None
        cookie, _ = load_cookie()
        if not isinstance(cookie, str) or not cookie:
            raise CredentialError("missing_credential", 401)
        index = CONFIG.get("auth_user")
        if index is None:
            return SessionCredential(cookie, 0)
        if not isinstance(index, (str, int)):
            raise CredentialError("invalid_credential", 400)
        return SessionCredential(cookie, int(index))

    def _session(self) -> NativeSession:
        credential = resolve_credential(self._presented(), self._legacy)
        return NativeSession(credential.cookie, str(credential.auth_user))

    def _authorized(self) -> bool:
        key = self._presented()
        if key and key.startswith("gemini-web:"):
            resolve_credential(key, self._legacy)
            return True
        return super()._authorized()

    def _failure(self, error: AccountError | CredentialError | VideoError | WorkerError) -> None:
        if isinstance(error, (CredentialError, VideoError, WorkerError)):
            code = error.status
        else:
            error = account_failure(error)
            code = error.status
        self.send_json({"error": {"code": code, "message": error.kind}}, code)

    def do_GET(self) -> None:
        path = urlsplit(self.path).path
        if path in {"/v1/models", "/v1beta/models"} and not (self._presented() or "").startswith("gemini-web:"):
            super().do_GET()
            return
        if path not in {"/v1/usage", "/v1/account-models", "/v1/models", "/v1beta/models"}:
            super().do_GET()
            return
        try:
            session = self._session()
            if path == "/v1/usage":
                usage = session.usage()
                result = asdict(usage)
                result["metrics"] = None if usage.metrics is None else [
                    {**asdict(metric), "usage_percent": metric.usage_percent}
                    for metric in usage.metrics
                ]
                self.send_json(result)
                return
            account = session.account_models()
            if path == "/v1/account-models":
                self.send_json(asdict(account))
                return
            records: list[ModelRow] = []
            if any(model.display_name == "3.8 Flash" for model in account.models):
                records.append({"id": FLASH_MODEL, "description": "Account-verified Gemini 3.8 Flash", "methods": ["generateContent", "streamGenerateContent"]})
            records.append({"id": MODEL, "description": "Gemini web video tool; native generateContent only", "methods": ["generateContent"]})
            if path == "/v1/models":
                self.send_json({"object": "list", "data": [{"id": row["id"], "object": "model", "owned_by": "google", "description": row["description"]} for row in records]})
            else:
                self.send_json({"models": [{"name": "models/" + row["id"], "displayName": row["id"], "description": row["description"], "supportedGenerationMethods": row["methods"]} for row in records]})
        except (AccountError, CredentialError, VideoError) as error:
            self._failure(error)
        except (BrokenPipeError, ConnectionResetError):
            return

    def do_POST(self) -> None:
        try:
            if self.path in {"/v1/session/inspect", "/v1/session/renew"}:
                self.close_connection = True
                token = self._presented() or ""
                _ = decode_token(token)
                self._credential_body()
                operation = "inspect" if self.path == "/v1/session/inspect" else "renew"
                self.send_json(run_credential_operation(operation, token))
                return
            if not self._authorized():
                self.send_json({"error": {"message": "invalid api key"}}, 401)
                return
            super().do_POST()
        except (CredentialError, AccountError, VideoError, WorkerError) as error:
            self._failure(error)
        except (BrokenPipeError, ConnectionResetError):
            return

    def _credential_body(self) -> None:
        lengths = self.headers.get_all("Content-Length", [])
        if "Transfer-Encoding" in self.headers or len(lengths) > 1:
            raise CredentialError("invalid_credential", 400)
        text = lengths[0] if lengths else "0"
        if not re.fullmatch(r"[0-9]{1,5}", text) or int(text) > MAX_TOKEN_LENGTH:
            raise CredentialError("invalid_credential", 400)
        size = int(text)
        if size:
            body = self.rfile.read(size)
            try:
                if len(body) != size or _decode_json(body) != {}:
                    raise CredentialError("invalid_credential", 400)
            except (ValueError, UnicodeError, RecursionError):
                raise CredentialError("invalid_credential", 400) from None

    def _handle_chat(self, body: bytes) -> None:
        request = self._parse_body(body)
        if (self._presented() or "").startswith("gemini-web:") or (
            isinstance(request, dict) and request.get("model") in {MODEL, FLASH_MODEL}
        ):
            self.send_json({"error": {"message": "Use Gemini-native endpoints on this private upstream"}}, 400)
            return
        super()._handle_chat(body)

    def _handle_responses(self, body: bytes) -> None:
        request = self._parse_body(body)
        if (self._presented() or "").startswith("gemini-web:") or (
            isinstance(request, dict) and request.get("model") in {MODEL, FLASH_MODEL}
        ):
            self.send_json({"error": {"message": "Use Gemini-native endpoints on this private upstream"}}, 400)
            return
        super()._handle_responses(body)

    def _cancelled(self) -> bool:
        ready, _, _ = select.select([self.connection], [], [], 0)
        return bool(ready) and not self.connection.recv(1, socket.MSG_PEEK)

    def _handle_google_generate(self, body: bytes, stream: bool) -> None:
        model = urlsplit(self.path).path.rsplit("/", 1)[-1].split(":", 1)[0]
        if model not in {MODEL, FLASH_MODEL}:
            if (self._presented() or "").startswith("gemini-web:"):
                self.send_json({"error": {"message": "Model not supported for web-session tokens"}}, 404)
                return
            super()._handle_google_generate(body, stream)
            return
        try:
            session = self._session()
            if model == MODEL:
                if stream:
                    raise VideoError("video_streaming_not_supported", 400)
                video = generate_video(session, parse_prompt(body), self._cancelled)
                response = {"modelVersion": MODEL, "candidates": [{"index": 0, "finishReason": "STOP", "content": {"role": "model", "parts": [{"inlineData": {"mimeType": "video/mp4", "data": base64.b64encode(video).decode("ascii")}}]}}]}
            else:
                request = self._parse_body(body)
                if not isinstance(request, dict):
                    raise VideoError("invalid_request", 400)
                prompt, images = google_contents_to_prompt(request)
                if not prompt.strip() or images:
                    raise VideoError("text_only_flash_request", 400)
                text = session.generate_flash(prompt)
                has_tools = request.get("tools") and request.get("toolConfig", {}).get("functionCallingConfig", {}).get("mode", "AUTO") != "NONE"
                text, calls = parse_google_function_calls(text) if has_tools else (text, [])
                parts = ([{"text": text}] if text else []) + [{"functionCall": call} for call in calls]
                response = {"modelVersion": FLASH_MODEL, "candidates": [{"index": 0, "finishReason": "STOP", "content": {"role": "model", "parts": parts}}]}
            if stream:
                self._start_sse()
                self.wfile.write(("data: " + json.dumps(response) + "\n\n").encode())
                self.wfile.flush()
            else:
                self.send_json(response)
        except (AccountError, CredentialError, VideoError) as error:
            self._failure(error)
        except (BrokenPipeError, ConnectionResetError):
            return
