"""Direct HTTP generation using the authenticated account's model capabilities."""

from __future__ import annotations

import json
import uuid
from contextlib import closing
from http.client import HTTPConnection, HTTPException, HTTPSConnection
from typing import final
from urllib.parse import urlencode, urlsplit

from .account import (
    AccountError,
    AccountModels,
    HttpSession,
    JsonValue,
    ModelCapability,
)
from .video import VideoError, at, response_bodies


@final
class NativeSession(HttpSession):
    def _post_generation(self, fields: list[JsonValue], models: AccountModels, model: ModelCapability) -> bytes:
        capacity = 2 if models.capacity_flags and 8 in models.capacity_flags else 1
        header: list[JsonValue] = [1, None, None, None, model.capability_id, None, None, 0, [4, 5, 6, 8], None, None, capacity, None, None, model.mode]
        params = {"bl": self._build, "hl": "en", "_reqid": str(self._request_id), "rt": "c"}
        if self._session_id:
            params["f.sid"] = self._session_id
        self._request_id += 100000
        data = urlencode({"f.req": json.dumps([None, json.dumps(fields)]), "at": self._xsrf}).encode()
        path = self._prefix + "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?" + urlencode(params)
        origin = urlsplit(self._origin)
        connection_type = HTTPSConnection if origin.scheme == "https" else HTTPConnection
        headers = self._request_headers()
        headers["x-goog-ext-525001261-jspb"] = json.dumps(header)
        headers["x-goog-ext-73010990-jspb"] = "[0,0,0]"
        try:
            with closing(connection_type(origin.netloc, timeout=None)) as connection:
                connection.request("POST", path, data, headers)
                with connection.getresponse() as response:
                    if response.status != 200:
                        raise AccountError("upstream_status", http_status=response.status)
                    return response.read()
        except (OSError, HTTPException, ValueError):
            raise AccountError("transport_failed") from None

    def submit_video(self, prompt: str) -> bytes:
        models = self.account_models()
        model = next((item for item in models.models if item.mode == 1), None)
        if model is None:
            raise AccountError("model_unavailable")
        fields = generation_fields(prompt, model.mode, 0)
        fields[0] = [prompt, 0, None, None, None, None, 0, None, None, [None, None, None, None, None, None, [[None, None, None, 1]]]]
        video_fields: dict[int, JsonValue] = {41: [1], 45: None, 49: 11, 54: [], 55: [[16]], 67: 0, 68: 1, 80: 1, 91: 0, 96: 0, 98: 1}
        for index, value in video_fields.items():
            fields[index] = value
        return self._post_generation(fields, models, model)

    def generate_flash(self, prompt: str) -> str:
        models = self.account_models()
        model = models.select_model("3.8 Flash")
        fields = generation_fields(prompt, model.mode, 4)
        fields[41], fields[45] = [1], 1
        raw = self._post_generation(fields, models, model)
        text = ""
        for body in response_bodies(raw):
            value = at(body, 4, 0, 1, 0)
            if isinstance(value, str) and value:
                text = value
        if not text:
            raise AccountError("invalid_response")
        return text

    def download_video(self, url: str) -> tuple[int, bytes]:
        target = urlsplit(url)
        host = target.hostname or ""
        authenticated_origin = host in {"gemini.google.com", "contribution.usercontent.google.com"}
        allowed = authenticated_origin or host.endswith((".googleusercontent.com", ".googlevideo.com"))
        if target.scheme != "https" or not allowed or target.username or target.password:
            raise VideoError("invalid_video_origin")
        headers = {"Referer": "https://gemini.google.com/"}
        if authenticated_origin:
            headers["Cookie"] = self._cookie
        path = target.path + ("?" + target.query if target.query else "")
        try:
            with closing(HTTPSConnection(target.netloc, timeout=None)) as connection:
                connection.request("GET", path, headers=headers)
                with connection.getresponse() as response:
                    return response.status, response.read()
        except (OSError, HTTPException, ValueError):
            raise VideoError("video_download_failed") from None


def generation_fields(prompt: str, mode: int, thinking: int) -> list[JsonValue]:
    fields: list[JsonValue] = [None] * 102
    values: dict[int, JsonValue] = {
        0: [prompt, 0, None, None, None, None, 0],
        1: ["en"], 2: ["", "", "", None, None, None, None, None, None, ""],
        6: [0], 7: 1, 10: 1, 11: 0, 17: [[thinking]], 18: 0, 27: 1,
        30: [4], 41: [1], 53: 0, 59: str(uuid.uuid4()), 61: [], 68: 1, 79: mode,
    }
    for index, value in values.items():
        fields[index] = value
    return fields
