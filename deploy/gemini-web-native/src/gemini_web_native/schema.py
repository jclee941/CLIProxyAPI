"""Gemini-native request and response shapes served by the bridge."""

from __future__ import annotations

from typing import Annotated, ClassVar, Final, Literal

from pydantic import BaseModel, ConfigDict, Field, StringConstraints

BRIDGE_MODEL: Final = "gemini-web-video"
VIDEO_MIME_TYPE: Final = "video/mp4"
SUPPORTED_IMAGE_MIME_TYPES: Final = frozenset(
    {"image/png", "image/jpeg", "image/webp"},
)

MAX_PROMPT_CHARS: Final = 8_000
MAX_PARTS: Final = 16
MAX_INLINE_IMAGE_BYTES: Final = 4 * 1024 * 1024

Base64Payload = Annotated[
    str,
    StringConstraints(min_length=1, max_length=MAX_INLINE_IMAGE_BYTES),
]


class InlineData(BaseModel):
    """Inline binary payload in Gemini `inlineData` shape."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, extra="forbid")

    mime_type: str = Field(alias="mimeType", min_length=1, max_length=128)
    data: Base64Payload


class RequestPart(BaseModel):
    """A single request part: either text or inline image data."""

    model_config: ClassVar[ConfigDict] = ConfigDict(
        frozen=True, extra="forbid", populate_by_name=True
    )

    text: str | None = Field(default=None, max_length=MAX_PROMPT_CHARS)
    inline_data: InlineData | None = Field(default=None, alias="inlineData")


class RequestContent(BaseModel):
    """One `contents` entry of a Gemini generateContent request."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, extra="forbid")

    role: Literal["user", "model"] | None = None
    parts: list[RequestPart] = Field(min_length=1, max_length=MAX_PARTS)


class GenerateContentRequest(BaseModel):
    """Gemini generateContent request accepted by the bridge."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, extra="forbid")

    contents: list[RequestContent] = Field(min_length=1, max_length=MAX_PARTS)
    model: Literal["gemini-web-video"] | None = None


class ResponseInlineData(BaseModel):
    """Inline binary payload returned to the caller."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, populate_by_name=True)

    mime_type: str = Field(serialization_alias="mimeType")
    data: str


class ResponsePart(BaseModel):
    """A single response part carrying text or downloaded media bytes."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, populate_by_name=True)

    text: str | None = None
    inline_data: ResponseInlineData | None = Field(default=None, serialization_alias="inlineData")


class ResponseContent(BaseModel):
    """Gemini response content block."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    role: Literal["model"] = "model"
    parts: list[ResponsePart]


class Candidate(BaseModel):
    """Gemini response candidate."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, populate_by_name=True)

    content: ResponseContent
    finish_reason: Literal["STOP"] = Field(
        default="STOP",
        serialization_alias="finishReason",
    )
    index: int = 0


class GenerateContentResponse(BaseModel):
    """Gemini generateContent response returned by the bridge."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, populate_by_name=True)

    candidates: list[Candidate]
    model_version: str = Field(
        default=BRIDGE_MODEL,
        serialization_alias="modelVersion",
    )


class ModelInfo(BaseModel):
    """A single entry of the models listing."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, populate_by_name=True)

    name: str
    display_name: str = Field(serialization_alias="displayName")
    description: str
    supported_generation_methods: list[str] = Field(
        serialization_alias="supportedGenerationMethods",
    )


class ModelListResponse(BaseModel):
    """Response of the models listing endpoint."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    models: list[ModelInfo]


class HealthResponse(BaseModel):
    """Liveness plus auth readiness, without any credential material."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, populate_by_name=True)

    status: Literal["ok"] = "ok"
    auth_configured: bool = Field(serialization_alias="authConfigured")
    upstream_ready: bool = Field(serialization_alias="upstreamReady")
