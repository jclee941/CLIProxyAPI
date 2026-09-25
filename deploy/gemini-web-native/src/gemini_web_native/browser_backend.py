"""Drives the Gemini web UI in an already logged-in browser over CDP."""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import TYPE_CHECKING, Final, assert_never
from urllib.parse import urljoin

import anyio
from playwright.async_api import Error as PlaywrightError
from playwright.async_api import async_playwright

from gemini_web_native.backend import (
    AuthUnavailableError,
    NoVideoProducedError,
    QuotaExceededError,
    UnsupportedInputError,
    UpstreamFailureError,
    VideoPrompt,
    VideoResult,
)

if TYPE_CHECKING:
    from playwright.async_api import Browser, Page, Playwright

    from gemini_web_native.config import BridgeConfig

GEMINI_APP_URL: Final = "https://gemini.google.com/app"
VIDEO_MENU_LABEL: Final = "동영상"
PROMPT_LABEL: Final = "Gemini 프롬프트 입력"
SEND_LABEL: Final = "메시지 보내기"
STOP_LABEL: Final = "대답 생성 중지"
RESPONSE_SELECTOR: Final = "model-response"
POLL_SECONDS: Final = 0.25
MP4_BOX_OFFSET: Final = 4
AUTH_PATTERN: Final = re.compile(r"로그인|본인 확인|sign in|verify", re.IGNORECASE)
QUOTA_PATTERN: Final = re.compile(r"한도|제한|limit|quota", re.IGNORECASE)


@dataclass(frozen=True, slots=True)
class Generating:
    """Gemini is still working on the reply."""


@dataclass(frozen=True, slots=True)
class VideoReady:
    """The reply rendered a playable video element."""

    src: str


@dataclass(frozen=True, slots=True)
class AuthChallenge:
    """The page asks the human to log in or pass a verification step."""


@dataclass(frozen=True, slots=True)
class QuotaHit:
    """The page shows a visible usage-limit notice."""


@dataclass(frozen=True, slots=True)
class TextOnlyReply:
    """Generation finished, but the reply carries no video."""


type PageState = Generating | VideoReady | AuthChallenge | QuotaHit | TextOnlyReply


async def _read_state(page: Page) -> PageState:
    if await page.get_by_role("dialog").filter(has_text=AUTH_PATTERN).count():
        return AuthChallenge()
    if await page.get_by_role("alert").filter(has_text=QUOTA_PATTERN).count():
        return QuotaHit()

    video = page.locator("video[src]").first
    if await video.count():
        src = await video.get_attribute("src")
        if src:
            return VideoReady(src=urljoin(page.url, src))
    if await page.get_by_role("button", name=STOP_LABEL).count():
        return Generating()
    if await page.locator(RESPONSE_SELECTOR).count():
        return TextOnlyReply()
    return Generating()


async def _reply_text(page: Page) -> str:
    reply = page.locator(RESPONSE_SELECTOR).first
    return await reply.inner_text() if await reply.count() else ""


async def _download(page: Page, src: str) -> bytes:
    response = await page.request.get(src)
    body = await response.body()
    if not response.ok or body[MP4_BOX_OFFSET : MP4_BOX_OFFSET + 4] != b"ftyp":
        raise NoVideoProducedError(summary=await _reply_text(page))
    return body


class BrowserVideoBackend:
    """Runs each request in its own tab of the operator's shared browser."""

    def __init__(self, config: BridgeConfig, app_url: str = GEMINI_APP_URL) -> None:
        """Bind the CDP endpoint of the shared browser and the Gemini entry point."""
        self._cdp_url: str = config.cdp_url
        self._app_url: str = app_url
        self._driver: Playwright | None = None
        self._browser: Browser | None = None
        self._turn: anyio.Lock = anyio.Lock()

    async def start(self) -> None:
        """Attach to the shared browser and confirm one owned tab is signed in."""
        browser = await self._attach()
        page = await browser.contexts[0].new_page()
        try:
            await self._open_video_surface(page)
        finally:
            with anyio.CancelScope(shield=True):
                await page.close()

    def is_ready(self) -> bool:
        """Report whether the shared browser is still attached."""
        return self._browser is not None and self._browser.is_connected()

    async def generate_video(self, prompt: VideoPrompt) -> VideoResult:
        """Ask Gemini for a video through the visible UI and download its bytes."""
        browser = await self._attach()
        async with self._turn:
            page = await browser.contexts[0].new_page()
            try:
                return await self._run_turn(page, prompt)
            finally:
                with anyio.CancelScope(shield=True):
                    await page.close()

    async def aclose(self) -> None:
        """Detach by stopping the owned driver; the shared browser keeps running."""
        driver, self._driver, self._browser = self._driver, None, None
        if driver is not None:
            await driver.stop()

    async def _attach(self) -> Browser:
        if self._browser is not None and self._browser.is_connected():
            return self._browser
        driver = self._driver or await async_playwright().start()
        self._driver = driver
        try:
            browser = await driver.chromium.connect_over_cdp(self._cdp_url, no_defaults=True)
        except PlaywrightError as exc:
            raise UpstreamFailureError(str(exc)) from None
        self._browser = browser
        return browser

    async def _open_video_surface(self, page: Page) -> None:
        try:
            _ = await page.goto(self._app_url)
        except PlaywrightError as exc:
            raise UpstreamFailureError(str(exc)) from None
        if await page.get_by_role("dialog").filter(has_text=AUTH_PATTERN).count():
            raise AuthUnavailableError(self._app_url)
        await page.get_by_role("link", name=VIDEO_MENU_LABEL, exact=True).click()

    async def _run_turn(self, page: Page, prompt: VideoPrompt) -> VideoResult:
        await self._open_video_surface(page)
        if prompt.images and not await page.locator('input[type="file"]').count():
            raise UnsupportedInputError(self._app_url)

        await page.get_by_role("textbox", name=PROMPT_LABEL).fill(prompt.text)
        for image in prompt.images:
            await page.locator('input[type="file"]').set_input_files(
                files=[{"name": "input.png", "mimeType": "image/png", "buffer": image}],
            )
        await page.get_by_role("button", name=SEND_LABEL).click()
        return await self._await_video(page)

    async def _await_video(self, page: Page) -> VideoResult:
        while True:
            try:
                state = await _read_state(page)
            except PlaywrightError as exc:
                raise UpstreamFailureError(str(exc)) from None

            match state:
                case VideoReady(src=src):
                    return VideoResult(summary=await _reply_text(page), video=await _download(page, src))
                case AuthChallenge():
                    raise AuthUnavailableError(page.url)
                case QuotaHit():
                    raise QuotaExceededError(page.url)
                case TextOnlyReply():
                    raise NoVideoProducedError(summary=await _reply_text(page))
                case Generating():
                    await anyio.sleep(POLL_SECONDS)
                case unreachable:
                    assert_never(unreachable)


def build_backend(config: BridgeConfig) -> BrowserVideoBackend:
    """Construct the shared-browser backend from operator configuration."""
    return BrowserVideoBackend(config)
