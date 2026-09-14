from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING

import pytest
from pydantic import SecretStr
from fastapi.testclient import TestClient

from gemini_web_native.app import create_app
from gemini_web_native.backend import (
    AuthUnavailableError,
    NoVideoProducedError,
    QuotaExceededError,
    UpstreamFailureError,
    VideoPrompt,
    VideoResult,
)
from gemini_web_native.config import BridgeConfig

if TYPE_CHECKING:
    from collections.abc import Iterator
    from pathlib import Path

    from fastapi import FastAPI

TEST_API_KEY = "test-bridge-key-1234"
MP4_BYTES = b"\x00\x00\x00\x20ftypisom-fake-video-bytes"


# MUTABLE_OK: records calls made by the app under test
@dataclass(slots=True)
class FakeBackend:
    """In-memory stand-in for the pinned Gemini web client."""

    result: VideoResult | None = None
    failure: Exception | None = None
    prompts: list[VideoPrompt] = field(default_factory=list)
    closed: bool = False
    ready: bool = True
    started: bool = False

    async def start(self) -> None:
        self.started = True

    async def generate_video(self, prompt: VideoPrompt) -> VideoResult:
        self.prompts.append(prompt)
        if self.failure is not None:
            raise self.failure
        if self.result is None:
            raise NoVideoProducedError(summary="no video in reply")
        return self.result

    def is_ready(self) -> bool:
        return self.ready

    async def aclose(self) -> None:
        self.closed = True


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


@pytest.fixture
def config() -> BridgeConfig:
    return BridgeConfig(api_key=SecretStr(TEST_API_KEY))


@pytest.fixture
def backend() -> FakeBackend:
    return FakeBackend(result=VideoResult(summary="Here is your video.", video=MP4_BYTES))


@pytest.fixture
def app(config: BridgeConfig, backend: FakeBackend) -> FastAPI:
    return create_app(config=config, backend_factory=lambda _: backend)


@pytest.fixture
def client(app: FastAPI) -> Iterator[TestClient]:
    with TestClient(app) as test_client:
        yield test_client


@pytest.fixture
def auth_headers() -> dict[str, str]:
    return {"x-goog-api-key": TEST_API_KEY}


@pytest.fixture
def config_file(tmp_path: Path) -> Path:
    return tmp_path / "config.json"


__all__ = [
    "MP4_BYTES",
    "TEST_API_KEY",
    "AuthUnavailableError",
    "FakeBackend",
    "QuotaExceededError",
    "UpstreamFailureError",
]
