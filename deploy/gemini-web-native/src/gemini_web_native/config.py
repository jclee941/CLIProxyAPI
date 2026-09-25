"""Bridge configuration parsed from an operator-supplied JSON file."""

from __future__ import annotations

import os
from pathlib import Path
from typing import ClassVar, Final

from pydantic import BaseModel, ConfigDict, Field, SecretStr, ValidationError

from gemini_web_native.schema import BRIDGE_MODEL

DEFAULT_CONFIG_PATH: Final = Path("/run/gemini-web-native/config.json")
CONFIG_PATH_ENV: Final = "GEMINI_NATIVE_CONFIG"
DEFAULT_CDP_URL: Final = "http://192.168.50.220:9222"

MAX_CONFIG_BYTES: Final = 64 * 1024


class ConfigError(Exception):
    """The bridge configuration is missing, unreadable, or structurally invalid.

    The message never carries file content, so a malformed credential file cannot
    leak into logs or a startup traceback.
    """

    def __init__(self, path: Path, reason: str) -> None:
        """Record the offending path and a content-free reason."""
        self.path: Path = path
        self.reason: str = reason
        super().__init__(f"invalid bridge config at {path}: {reason}")


class BridgeConfig(BaseModel):
    """Bridge API key and the shared browser endpoint, never Google credentials."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, extra="forbid")

    api_key: SecretStr = Field(min_length=8)
    cdp_url: str = Field(default=DEFAULT_CDP_URL, min_length=1)
    model: str = Field(default=BRIDGE_MODEL, min_length=1)


def config_path_from_env(environ: dict[str, str] | None = None) -> Path:
    """Resolve the config path, defaulting to the runtime tmpfs location."""
    env = environ if environ is not None else dict(os.environ)
    raw = env.get(CONFIG_PATH_ENV, "").strip()
    return Path(raw) if raw else DEFAULT_CONFIG_PATH


def load_config(path: Path) -> BridgeConfig:
    """Read and parse the bridge config.

    Raises:
        ConfigError: the file is absent, oversized, or does not match the schema.

    """
    try:
        stat = path.stat()
    except OSError as exc:
        raise ConfigError(path, f"cannot stat config: {exc.strerror or 'unreadable'}") from None

    if stat.st_size > MAX_CONFIG_BYTES:
        raise ConfigError(path, "config exceeds the size limit")

    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise ConfigError(path, f"cannot read config: {exc.strerror or 'unreadable'}") from None

    try:
        return BridgeConfig.model_validate_json(raw)
    except ValidationError as exc:
        fields = sorted({str(error["loc"][0]) for error in exc.errors() if error["loc"]})
        detail = ", ".join(fields) if fields else "document"
        raise ConfigError(path, f"invalid or missing fields: {detail}") from None
