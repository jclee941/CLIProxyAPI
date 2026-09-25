"""Process entry point: load config, attach to the shared browser, serve HTTP."""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Final

import uvicorn

from gemini_web_native.app import create_app
from gemini_web_native.browser_backend import build_backend
from gemini_web_native.config import config_path_from_env, load_config

if TYPE_CHECKING:
    from fastapi import FastAPI

DEFAULT_PORT: Final = 8082

logger = logging.getLogger("gemini_web_native")


def build_app() -> FastAPI:
    """Assemble the application from the operator-supplied config file."""
    return create_app(config=load_config(config_path_from_env()), backend_factory=build_backend)


def main() -> None:
    """Run the bridge with uvicorn on the bridge port."""
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    uvicorn.run(build_app(), host="0.0.0.0", port=DEFAULT_PORT)  # noqa: S104 - container network


if __name__ == "__main__":
    main()
