from __future__ import annotations

import base64
from typing import TYPE_CHECKING

import pytest
from fastapi.testclient import TestClient

from gemini_web_native.backend import (
    AuthUnavailableError,
    QuotaExceededError,
    UpstreamFailureError,
    VideoResult,
)
from gemini_web_native.schema import BRIDGE_MODEL, VIDEO_MIME_TYPE

from .conftest import MP4_BYTES, TEST_API_KEY, FakeBackend

if TYPE_CHECKING:
    from fastapi import FastAPI

GENERATE_URL = f"/v1beta/models/{BRIDGE_MODEL}:generateContent"
PROMPT_BODY = {"contents": [{"role": "user", "parts": [{"text": "a cat surfing a wave"}]}]}


def test_health_reports_auth_readiness_without_credentials(client: TestClient) -> None:
    response = client.get("/health")

    assert response.status_code == 200
    body = response.json()
    assert body == {"status": "ok", "authConfigured": True, "upstreamReady": True}
    assert TEST_API_KEY not in response.text


def test_models_requires_api_key(client: TestClient) -> None:
    assert client.get("/v1beta/models").status_code == 401


def test_models_rejects_query_string_key(client: TestClient) -> None:
    assert client.get("/v1beta/models", params={"key": TEST_API_KEY}).status_code == 401


def test_models_rejects_wrong_api_key(client: TestClient) -> None:
    response = client.get("/v1beta/models", headers={"x-goog-api-key": "wrong-key-value"})

    assert response.status_code == 401


def test_models_lists_only_the_bridge_video_model(
    client: TestClient,
    auth_headers: dict[str, str],
) -> None:
    response = client.get("/v1beta/models", headers=auth_headers)

    assert response.status_code == 200
    models = response.json()["models"]
    assert [model["name"] for model in models] == [f"models/{BRIDGE_MODEL}"]
    assert models[0]["supportedGenerationMethods"] == ["generateContent"]


def test_bearer_token_is_accepted(client: TestClient) -> None:
    response = client.get("/v1beta/models", headers={"authorization": f"Bearer {TEST_API_KEY}"})

    assert response.status_code == 200


def test_generate_returns_downloaded_video_bytes_and_summary(
    client: TestClient,
    auth_headers: dict[str, str],
    backend: FakeBackend,
) -> None:
    response = client.post(GENERATE_URL, json=PROMPT_BODY, headers=auth_headers)

    assert response.status_code == 200
    parts = response.json()["candidates"][0]["content"]["parts"]
    inline = next(part["inlineData"] for part in parts if "inlineData" in part)
    assert inline["mimeType"] == VIDEO_MIME_TYPE
    assert base64.b64decode(inline["data"]) == MP4_BYTES
    assert any(part.get("text") for part in parts)
    assert backend.prompts[0].text == "a cat surfing a wave"


def test_generate_forwards_supported_inline_image(
    client: TestClient,
    auth_headers: dict[str, str],
    backend: FakeBackend,
) -> None:
    image = base64.b64encode(b"fake-png-bytes").decode("ascii")
    body = {
        "contents": [
            {
                "role": "user",
                "parts": [
                    {"text": "animate this"},
                    {"inlineData": {"mimeType": "image/png", "data": image}},
                ],
            },
        ],
    }

    response = client.post(GENERATE_URL, json=body, headers=auth_headers)

    assert response.status_code == 200
    assert backend.prompts[0].images == (b"fake-png-bytes",)


def test_generate_rejects_unsupported_inline_mime_type(
    client: TestClient,
    auth_headers: dict[str, str],
) -> None:
    body = {
        "contents": [
            {
                "parts": [
                    {"text": "animate"},
                    {
                        "inlineData": {
                            "mimeType": "application/zip",
                            "data": base64.b64encode(b"zip").decode("ascii"),
                        },
                    },
                ],
            },
        ],
    }

    response = client.post(GENERATE_URL, json=body, headers=auth_headers)

    assert response.status_code == 415


def test_generate_rejects_missing_prompt_text(
    client: TestClient,
    auth_headers: dict[str, str],
) -> None:
    response = client.post(
        GENERATE_URL,
        json={"contents": [{"parts": [{"text": "   "}]}]},
        headers=auth_headers,
    )

    assert response.status_code == 422


def test_generate_rejects_malformed_body_without_echoing_input(
    client: TestClient,
    auth_headers: dict[str, str],
) -> None:
    response = client.post(
        GENERATE_URL,
        json={"contents": "leaky-sentinel-value"},
        headers=auth_headers,
    )

    assert response.status_code == 422
    assert "leaky-sentinel-value" not in response.text


def test_generate_rejects_oversized_body(
    client: TestClient,
    auth_headers: dict[str, str],
) -> None:
    payload = "x" * (6 * 1024 * 1024)

    response = client.post(
        GENERATE_URL,
        content=payload.encode("ascii"),
        headers={**auth_headers, "content-type": "application/json"},
    )

    assert response.status_code == 413


def test_unknown_model_fails_explicitly(
    client: TestClient,
    auth_headers: dict[str, str],
) -> None:
    response = client.post(
        "/v1beta/models/gemini-3-pro:generateContent",
        json=PROMPT_BODY,
        headers=auth_headers,
    )

    assert response.status_code == 404


def test_generate_requires_api_key(client: TestClient) -> None:
    assert client.post(GENERATE_URL, json=PROMPT_BODY).status_code == 401


def test_plain_text_reply_without_video_is_typed_no_video_failure(
    client: TestClient,
    auth_headers: dict[str, str],
    backend: FakeBackend,
) -> None:
    backend.result = None

    response = client.post(GENERATE_URL, json=PROMPT_BODY, headers=auth_headers)

    assert response.status_code == 422
    assert response.json()["error"]["status"] == "NO_VIDEO_GENERATED"


@pytest.mark.parametrize(
    ("failure", "expected"),
    [
        (AuthUnavailableError(), (503, "AUTH_UNAVAILABLE")),
        (QuotaExceededError(), (429, "QUOTA_EXCEEDED")),
        (UpstreamFailureError(), (502, "UPSTREAM_FAILED")),
    ],
)
def test_upstream_failures_map_to_distinct_statuses(
    client: TestClient,
    auth_headers: dict[str, str],
    backend: FakeBackend,
    failure: Exception,
    expected: tuple[int, str],
) -> None:
    backend.failure = failure
    expected_status, expected_code = expected

    response = client.post(GENERATE_URL, json=PROMPT_BODY, headers=auth_headers)

    assert response.status_code == expected_status
    assert response.json()["error"]["status"] == expected_code


def test_health_reports_upstream_not_ready(client: TestClient, backend: FakeBackend) -> None:
    backend.ready = False

    body = client.get("/health").json()

    assert body["status"] == "ok"
    assert body["upstreamReady"] is False


def test_lifespan_closes_backend_client(app: FastAPI, backend: FakeBackend) -> None:
    with TestClient(app) as running:
        assert running.get("/health").status_code == 200
        assert backend.closed is False

    assert backend.closed is True


def test_successful_generation_reuses_one_backend(
    client: TestClient,
    auth_headers: dict[str, str],
    backend: FakeBackend,
) -> None:
    backend.result = VideoResult(summary="done", video=MP4_BYTES)

    for _ in range(2):
        assert client.post(GENERATE_URL, json=PROMPT_BODY, headers=auth_headers).status_code == 200

    assert len(backend.prompts) == 2
