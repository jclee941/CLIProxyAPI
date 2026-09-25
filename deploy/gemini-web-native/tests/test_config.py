from __future__ import annotations

from typing import TYPE_CHECKING

import pytest

from gemini_web_native.config import (
    DEFAULT_CDP_URL,
    DEFAULT_CONFIG_PATH,
    ConfigError,
    config_path_from_env,
    load_config,
)
from gemini_web_native.schema import BRIDGE_MODEL

if TYPE_CHECKING:
    from pathlib import Path


def test_load_config_parses_the_bridge_key_and_defaults(config_file: Path) -> None:
    _ = config_file.write_text('{"api_key": "abcd1234efgh"}', encoding="utf-8")

    config = load_config(config_file)

    assert config.api_key.get_secret_value() == "abcd1234efgh"
    assert config.cdp_url == DEFAULT_CDP_URL
    assert config.model == BRIDGE_MODEL


def test_load_config_rejects_short_key_without_echoing_content(config_file: Path) -> None:
    _ = config_file.write_text('{"api_key": "leaky-x"}', encoding="utf-8")

    with pytest.raises(ConfigError) as excinfo:
        _ = load_config(config_file)

    assert "api_key" in str(excinfo.value)
    assert "leaky-x" not in str(excinfo.value)


def test_load_config_rejects_absent_file(tmp_path: Path) -> None:
    with pytest.raises(ConfigError):
        _ = load_config(tmp_path / "absent.json")


def test_config_path_defaults_to_runtime_location(tmp_path: Path) -> None:
    override = tmp_path / "override.json"

    assert config_path_from_env({}) == DEFAULT_CONFIG_PATH
    assert config_path_from_env({"GEMINI_NATIVE_CONFIG": str(override)}) == override
