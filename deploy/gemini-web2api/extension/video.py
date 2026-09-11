"""Decode the Gemini web video tool and poll the same HTTP conversation turn."""

from __future__ import annotations

import json
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Final, Literal, Protocol

from .account import JsonValue

MODEL = "gemini-web-omni"
JSON_DECODE: Final[Callable[[str], JsonValue]] = json.JSONDecoder().decode


class VideoError(Exception):
    def __init__(self, kind: str, status: int = 502) -> None:
        self.kind: str = kind
        self.status: int = status
        super().__init__(kind)


def at(value: JsonValue, *path: int) -> JsonValue:
    for index in path:
        if not isinstance(value, list) or len(value) <= index:
            return None
        value = value[index]
    return value


def jspb_field(value: JsonValue, index: int) -> JsonValue:
    direct = at(value, index)
    if direct is not None:
        return direct
    if isinstance(value, list) and value and isinstance(value[-1], dict):
        return value[-1].get(str(index + 1))
    return None


def parse_prompt(raw: bytes) -> str:
    if len(raw) > 5 * 1024 * 1024:
        raise VideoError("request_too_large", 413)
    try:
        body = JSON_DECODE(raw.decode())
    except (ValueError, UnicodeError):
        raise VideoError("invalid_json", 400) from None
    if not isinstance(body, dict) or set(body) - {"model", "contents", "generationConfig"}:
        raise VideoError("unsupported_video_request", 400)
    if body.get("model", MODEL) != MODEL or body.get("generationConfig", {}) != {}:
        raise VideoError("unsupported_video_options", 400)
    contents = body.get("contents")
    if not isinstance(contents, list) or len(contents) != 1:
        raise VideoError("single_user_turn_required", 400)
    turn = contents[0]
    if not isinstance(turn, dict) or set(turn) - {"role", "parts"} or turn.get("role", "user") != "user":
        raise VideoError("single_user_turn_required", 400)
    parts = turn.get("parts")
    if not isinstance(parts, list) or not parts:
        raise VideoError("text_prompt_required", 400)
    texts: list[str] = []
    for part in parts:
        if not isinstance(part, dict) or set(part) != {"text"} or not isinstance(part["text"], str):
            raise VideoError("text_only_video_request", 400)
        texts.append(part["text"])
    prompt = "\n".join(texts)
    if not prompt.strip() or len(prompt) > 8000:
        raise VideoError("invalid_prompt_length", 400)
    return prompt


@dataclass(frozen=True, slots=True)
class VideoPending:
    state: Literal["pending"] = "pending"


@dataclass(frozen=True, slots=True)
class VideoReady:
    url: str = field(repr=False)
    state: Literal["ready"] = "ready"


def parse_video_candidate(candidate: JsonValue) -> VideoPending | VideoReady:
    video = jspb_field(at(candidate, 12), 59)
    url = at(video, 0, 0, 0, 0, 7, 1)
    if isinstance(url, str) and url.startswith("https://"):
        return VideoReady(url)
    text = at(candidate, 1, 0)
    if isinstance(text, str) and "googleusercontent.com/video_gen_chip/" in text:
        return VideoPending()
    raise VideoError("no_video_generated", 422)


def response_bodies(raw: bytes) -> list[JsonValue]:
    bodies: list[JsonValue] = []
    for line in raw.splitlines():
        if not line.startswith(b"["):
            continue
        try:
            entries = JSON_DECODE(line.decode())
            if not isinstance(entries, list):
                raise VideoError("invalid_upstream_frame")
            for entry in entries:
                encoded = at(entry, 2)
                if at(entry, 0) == "wrb.fr" and isinstance(encoded, str):
                    bodies.append(JSON_DECODE(encoded))
        except (ValueError, UnicodeError):
            raise VideoError("invalid_upstream_frame") from None
    if not bodies:
        raise VideoError("missing_upstream_response")
    return bodies


class VideoTransport(Protocol):
    def submit_video(self, prompt: str) -> bytes: ...
    def rpc(self, rpcid: str, args: JsonValue, source_path: str = "/app") -> JsonValue: ...
    def download_video(self, url: str) -> tuple[int, bytes]: ...


def generate_video(
    transport: VideoTransport,
    prompt: str,
    cancelled: Callable[[], bool],
) -> bytes:
    frames = response_bodies(transport.submit_video(prompt))
    cid: str | None = None
    rid: str | None = None
    candidate: JsonValue = None
    for frame in frames:
        frame_cid, frame_rid = at(frame, 1, 0), at(frame, 1, 1)
        if isinstance(frame_cid, str) and isinstance(frame_rid, str):
            cid, rid = frame_cid, frame_rid
        if at(frame, 4, 0) is not None:
            candidate = at(frame, 4, 0)
    if not cid or not rid or candidate is None:
        raise VideoError("missing_video_operation")
    state = parse_video_candidate(candidate)
    while not cancelled():
        if isinstance(state, VideoReady):
            status, content = transport.download_video(state.url)
            if status == 200 and content[4:8] == b"ftyp":
                return content
            if status != 206:
                raise VideoError("invalid_video_download")
        time.sleep(10)
        if cancelled():
            break
        turns = transport.rpc("hNvQHb", [cid, 1, None, 1, [1], [4], None, 1])
        if at(turns, 0, 0, 0, 1) != rid:
            raise VideoError("video_operation_mismatch")
        candidate = at(turns, 0, 0, 3, 0, 0)
        state = parse_video_candidate(candidate)
    raise VideoError("caller_disconnected", 499)
