"""HTTP surface of the Gemini-native video bridge."""

from __future__ import annotations

import base64
import binascii
import hmac
import logging
from collections.abc import AsyncGenerator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from typing import Annotated, Final

import anyio
from fastapi import Depends, FastAPI, Header, HTTPException, Request, Response, status
from fastapi.responses import JSONResponse
from pydantic import ValidationError

from gemini_web_native.backend import (
    AuthUnavailableError,
    NoVideoProducedError,
    QuotaExceededError,
    UnsupportedInputError,
    UpstreamFailureError,
    VideoBackend,
    VideoPrompt,
    VideoResult,
)
from gemini_web_native.config import BridgeConfig
from gemini_web_native.schema import (
    BRIDGE_MODEL,
    SUPPORTED_IMAGE_MIME_TYPES,
    VIDEO_MIME_TYPE,
    Candidate,
    GenerateContentRequest,
    GenerateContentResponse,
    HealthResponse,
    ModelInfo,
    ModelListResponse,
    ResponseContent,
    ResponseInlineData,
    ResponsePart,
)

MAX_BODY_BYTES: Final = 5 * 1024 * 1024
BEARER_PREFIX: Final = "Bearer "

logger = logging.getLogger("gemini_web_native")

BackendFactory = Callable[[BridgeConfig], VideoBackend]
BackendFailure = (
    NoVideoProducedError
    | AuthUnavailableError
    | QuotaExceededError
    | UnsupportedInputError
    | UpstreamFailureError
)


def _error(code: int, api_status: str, message: str) -> JSONResponse:
    return JSONResponse(
        status_code=code,
        content={"error": {"code": code, "status": api_status, "message": message}},
    )


def _presented_key(api_key_header: str | None, authorization: str | None) -> str | None:
    if api_key_header:
        return api_key_header
    if authorization and authorization.startswith(BEARER_PREFIX):
        return authorization.removeprefix(BEARER_PREFIX).strip() or None
    return None


async def _read_bounded_body(request: Request) -> bytes:
    chunks: list[bytes] = []
    size = 0
    async for chunk in request.stream():
        size += len(chunk)
        if size > MAX_BODY_BYTES:
            raise HTTPException(
                status_code=status.HTTP_413_CONTENT_TOO_LARGE,
                detail="request body exceeds the bridge limit",
            )
        chunks.append(chunk)
    return b"".join(chunks)


def _decode_image(mime_type: str, data: str) -> bytes:
    if mime_type not in SUPPORTED_IMAGE_MIME_TYPES:
        raise HTTPException(
            status_code=status.HTTP_415_UNSUPPORTED_MEDIA_TYPE,
            detail=f"inline mime type is not supported: {mime_type}",
        )
    try:
        return base64.b64decode(data, validate=True)
    except (binascii.Error, ValueError):
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail="inline image data is not valid base64",
        ) from None


def _parse_prompt(payload: GenerateContentRequest) -> VideoPrompt:
    texts: list[str] = []
    images: list[bytes] = []
    for content in payload.contents:
        for part in content.parts:
            if part.text is not None and part.text.strip():
                texts.append(part.text.strip())
            if part.inline_data is not None:
                images.append(_decode_image(part.inline_data.mime_type, part.inline_data.data))

    if not texts:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail="request carries no prompt text",
        )

    return VideoPrompt(text="\n".join(texts), images=tuple(images))


def _video_response(result: VideoResult) -> GenerateContentResponse:
    parts = [
        ResponsePart(text=result.summary),
        ResponsePart(
            inline_data=ResponseInlineData(
                mime_type=VIDEO_MIME_TYPE,
                data=base64.b64encode(result.video).decode("ascii"),
            ),
        ),
    ]
    return GenerateContentResponse(candidates=[Candidate(content=ResponseContent(parts=parts))])


def _failure_response(exc: BackendFailure) -> JSONResponse:
    match exc:
        case NoVideoProducedError():
            logger.warning("gemini.no_video")
            return _error(
                status.HTTP_422_UNPROCESSABLE_CONTENT,
                "NO_VIDEO_GENERATED",
                "Gemini replied without a generated video",
            )
        case AuthUnavailableError():
            logger.error("gemini.auth_unavailable")
            return _error(
                status.HTTP_503_SERVICE_UNAVAILABLE,
                "AUTH_UNAVAILABLE",
                "Gemini web credentials are not usable",
            )
        case QuotaExceededError():
            logger.warning("gemini.quota_exceeded")
            return _error(
                status.HTTP_429_TOO_MANY_REQUESTS,
                "QUOTA_EXCEEDED",
                "Gemini usage limit reached",
            )
        case UnsupportedInputError():
            logger.warning("gemini.unsupported_input")
            return _error(
                status.HTTP_415_UNSUPPORTED_MEDIA_TYPE,
                "UNSUPPORTED_INPUT",
                "the Gemini video surface cannot accept this attachment",
            )
        case UpstreamFailureError():
            logger.error("gemini.upstream_failed")
            return _error(
                status.HTTP_502_BAD_GATEWAY,
                "UPSTREAM_FAILED",
                "Gemini did not return a usable result",
            )


async def _run_generation(backend: VideoBackend, raw: bytes) -> Response:
    try:
        payload = GenerateContentRequest.model_validate_json(raw)
    except ValidationError:
        return _error(
            status.HTTP_422_UNPROCESSABLE_CONTENT,
            "INVALID_ARGUMENT",
            "request body does not match the Gemini generateContent schema",
        )

    prompt = _parse_prompt(payload)
    try:
        result = await backend.generate_video(prompt)
    except (
        NoVideoProducedError,
        AuthUnavailableError,
        QuotaExceededError,
        UnsupportedInputError,
        UpstreamFailureError,
    ) as exc:
        return _failure_response(exc)

    return JSONResponse(
        content=_video_response(result).model_dump(by_alias=True, exclude_none=True),
    )


def _make_lifespan(
    config: BridgeConfig,
    backend_factory: BackendFactory,
    backends: dict[str, VideoBackend],
) -> Callable[[FastAPI], AbstractAsyncContextManager[None]]:
    @asynccontextmanager
    async def lifespan(_app: FastAPI) -> AsyncGenerator[None]:
        backend = backend_factory(config)
        backends["current"] = backend
        try:
            await backend.start()
        except (AuthUnavailableError, UpstreamFailureError):
            # Startup keeps serving so /health can report the unusable credentials
            # instead of crash-looping the container.
            logger.exception("gemini.startup_failed")
        try:
            yield
        finally:
            _dropped = backends.pop("current", None)
            with anyio.CancelScope(shield=True):
                await backend.aclose()

    return lifespan


def create_app(config: BridgeConfig, backend_factory: BackendFactory) -> FastAPI:
    """Build the bridge application around a backend factory."""
    backends: dict[str, VideoBackend] = {}

    lifespan = _make_lifespan(config, backend_factory, backends)

    app = FastAPI(title="Gemini web native video bridge", lifespan=lifespan)

    def get_backend() -> VideoBackend:
        backend = backends.get("current")
        if backend is None:
            raise HTTPException(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
                detail="bridge backend is not running",
            )
        return backend

    def require_api_key(
        x_goog_api_key: Annotated[str | None, Header()] = None,
        authorization: Annotated[str | None, Header()] = None,
    ) -> None:
        presented = _presented_key(x_goog_api_key, authorization)
        expected = config.api_key.get_secret_value()
        if presented is None or not hmac.compare_digest(presented, expected):
            raise HTTPException(
                status_code=status.HTTP_401_UNAUTHORIZED,
                detail="a valid API key is required in x-goog-api-key or Authorization",
            )

    @app.get("/health", response_model=HealthResponse, response_model_by_alias=True)
    async def health() -> HealthResponse:
        backend = backends.get("current")
        return HealthResponse(
            auth_configured=bool(config.api_key.get_secret_value()),
            upstream_ready=backend is not None and backend.is_ready(),
        )

    @app.get(
        "/v1beta/models",
        response_model=ModelListResponse,
        response_model_by_alias=True,
        dependencies=[Depends(require_api_key)],
    )
    async def list_models() -> ModelListResponse:
        return ModelListResponse(
            models=[
                ModelInfo(
                    name=f"models/{BRIDGE_MODEL}",
                    display_name="Gemini web native video",
                    description="Native Gemini web video generation returning downloaded mp4 bytes",
                    supported_generation_methods=["generateContent"],
                ),
            ],
        )

    @app.post(
        "/v1beta/models/{model_id}:generateContent",
        dependencies=[Depends(require_api_key)],
    )
    async def generate_content(
        model_id: str,
        request: Request,
    ) -> Response:
        if model_id != BRIDGE_MODEL:
            return _error(
                status.HTTP_404_NOT_FOUND,
                "MODEL_NOT_FOUND",
                f"unknown model: {model_id}",
            )

        return await _run_generation(get_backend(), await _read_bounded_body(request))

    return app
