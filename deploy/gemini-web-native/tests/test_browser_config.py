from __future__ import annotations

from typing import TYPE_CHECKING

import pytest
from pydantic import SecretStr, ValidationError

from gemini_web_native.config import BridgeConfig

if TYPE_CHECKING:
    from fastapi.testclient import TestClient

from .conftest import TEST_API_KEY
from .test_http_contract import GENERATE_URL, PROMPT_BODY


def test_config_when_browser_session_needs_no_google_credentials() -> None:
    # Given / When: only a bridge key, never Google credentials.
    config = BridgeConfig.model_validate({"api_key": TEST_API_KEY})
    # Then: repr-safe key and the authorized CDP default.
    assert isinstance(config.api_key, SecretStr)
    assert TEST_API_KEY not in repr(config)
    assert config.cdp_url == "http://192.168.50.220:9222"


def test_config_when_obsolete_credentials_are_rejected() -> None:
    # Given / When / Then: old cookie configuration is not a second backend.
    with pytest.raises(ValidationError):
        _ = BridgeConfig.model_validate({
            "api_key": TEST_API_KEY, "secure_1psid": "obsolete", "secure_1psidts": "obsolete",
        })


@pytest.mark.parametrize("model", ["gemini-web-video", "other-model"])
def test_generate_when_proxy_injects_top_level_model(client: TestClient, model: str) -> None:
    # Given / When: the proxy adds its selected model to the native body.
    response = client.post(
        GENERATE_URL, json={**PROMPT_BODY, "model": model},
        headers={"x-goog-api-key": TEST_API_KEY},
    )
    # Then: accept only the advertised model.
    assert response.status_code == (200 if model == "gemini-web-video" else 422)
