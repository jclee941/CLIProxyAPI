from __future__ import annotations

from typing import TYPE_CHECKING

import anyio
import pytest
from pydantic import SecretStr

from gemini_web_native.backend import (
    AuthUnavailableError, NoVideoProducedError, QuotaExceededError, VideoPrompt,
)
from gemini_web_native.browser_backend import BrowserVideoBackend
from gemini_web_native.config import BridgeConfig

from .browser_fixture import local_browser
from .conftest import TEST_API_KEY

if TYPE_CHECKING:
    from pathlib import Path


@pytest.mark.anyio
async def test_download_when_real_browser_renders_video_preserves_shared_lifetime(tmp_path: Path) -> None:
    # Given: an isolated CDP browser with an unrelated existing tab.
    async with local_browser(tmp_path) as fixture:
        original = fixture.context.pages[0]
        backend = BrowserVideoBackend(
            BridgeConfig(api_key=SecretStr(TEST_API_KEY), cdp_url=fixture.cdp_url),
            app_url=fixture.app_url,
        )
        # When: the adapter drives the visible UI and downloads the rendered media.
        result = await backend.generate_video(VideoPrompt(text="local fixture clip"))
        await backend.aclose()
        # Then: actual bytes, not a protected URL; only owned tabs/driver are closed.
        assert result.video == fixture.video
        assert fixture.context.pages == [original]
        assert not original.is_closed()
        assert await original.title() == ""


@pytest.mark.parametrize("mode,expected", [
    ("auth", AuthUnavailableError), ("challenge", AuthUnavailableError),
    ("quota", QuotaExceededError), ("empty", NoVideoProducedError),
    ("invalid", NoVideoProducedError),
])
@pytest.mark.anyio
async def test_failure_when_visible_terminal_ui_has_no_usable_video(
    tmp_path: Path, mode: str, expected: type[Exception],
) -> None:
    # Given: terminal UI rather than successful video output.
    async with local_browser(tmp_path) as fixture:
        backend = BrowserVideoBackend(
            BridgeConfig(api_key=SecretStr(TEST_API_KEY), cdp_url=fixture.cdp_url),
            app_url=f"{fixture.app_url}?mode={mode}",
        )
        # When / Then: a typed failure, with the request tab cleaned up.
        with pytest.raises(expected):
            _ = await backend.generate_video(VideoPrompt(text="local clip"))
        assert len(fixture.context.pages) == 1


@pytest.mark.anyio
async def test_cancel_when_pending_closes_only_owned_tab(tmp_path: Path) -> None:
    # Given: generation does not finish on its own.
    async with local_browser(tmp_path) as fixture:
        backend = BrowserVideoBackend(
            BridgeConfig(api_key=SecretStr(TEST_API_KEY), cdp_url=fixture.cdp_url),
            app_url=f"{fixture.app_url}?mode=pending",
        )
        async def generate() -> None:
            _ = await backend.generate_video(VideoPrompt(text="pending"))

        # When: caller cancellation after the UI accepted the prompt.
        async with anyio.create_task_group() as group:
            async with fixture.context.expect_page() as new_page:
                group.start_soon(generate)
            page = await new_page.value
            await page.locator('body[data-submitted="true"]').wait_for()
            group.cancel_scope.cancel()
        # Then: cancellation is not a successful empty result; shared tab survives.
        assert len(fixture.context.pages) == 1
