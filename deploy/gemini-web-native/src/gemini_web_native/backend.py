"""Backend contract shared by the real Gemini adapter and test fakes."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass(frozen=True, slots=True)
class VideoPrompt:
    """A parsed video generation request."""

    text: str
    images: tuple[bytes, ...] = ()


@dataclass(frozen=True, slots=True)
class VideoResult:
    """A successful generation: the model's summary plus downloaded video bytes."""

    summary: str
    video: bytes


class BackendError(Exception):
    """Base class for failures the HTTP boundary translates into a status code."""


class AuthUnavailableError(BackendError):
    """The supplied Gemini web credentials were rejected or are not usable."""


class QuotaExceededError(BackendError):
    """The account hit a Gemini usage limit."""


class UnsupportedInputError(BackendError):
    """The visible Gemini UI cannot accept the supplied attachment."""


class UpstreamFailureError(BackendError):
    """Gemini answered, but not with a result this bridge can use."""


@dataclass(frozen=True, slots=True)
class NoVideoProducedError(BackendError):
    """Gemini replied with text only; no video was generated."""

    summary: str


@runtime_checkable
class VideoBackend(Protocol):
    """Capability the HTTP layer depends on."""

    async def start(self) -> None:
        """Acquire the upstream session once at startup."""
        ...

    async def generate_video(self, prompt: VideoPrompt) -> VideoResult:
        """Generate a video and return its downloaded bytes."""
        ...

    def is_ready(self) -> bool:
        """Report whether the upstream session is initialized."""
        ...

    async def aclose(self) -> None:
        """Release the upstream session."""
        ...
