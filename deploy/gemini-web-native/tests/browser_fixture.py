from __future__ import annotations

import subprocess
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from socket import socket

import anyio
import uvicorn
from anyio.streams.text import TextReceiveStream
from fastapi import FastAPI, Response
from fastapi.responses import HTMLResponse
from playwright.async_api import BrowserContext, async_playwright

CHROME = "/usr/bin/google-chrome"
FFMPEG = "/usr/bin/ffmpeg"
DEVTOOLS_MARKER = "DevTools listening on "

HTML = """<!doctype html><html lang="ko"><body>
<a href="#video" onclick="this.dataset.selected='true'">동영상</a>
<textarea aria-label="Gemini 프롬프트 입력"></textarea>
<button aria-label="메시지 보내기" onclick="generate()">Send</button>
<div id="result"></div>
<script>
const mode = 'MODE';
function finish() {
  const result = document.querySelector('#result');
  if (mode === 'quota') result.innerHTML = '<div role="alert">동영상 생성 한도에 도달했습니다</div>';
  else if (mode === 'challenge') result.innerHTML = '<div role="dialog">본인 확인이 필요합니다</div>';
  else if (mode === 'empty') result.innerHTML = '<model-response>동영상을 만들 수 없습니다</model-response>';
  else if (mode === 'invalid') result.innerHTML = '<video controls src="/app?mode=auth"></video>';
  else result.innerHTML = '<video controls src="/clip.mp4"></video>';
  document.querySelector('button').setAttribute('aria-label', '메시지 보내기');
}
function generate() {
  if (!document.querySelector('a').dataset.selected) throw new Error('video mode required');
  document.querySelector('button').setAttribute('aria-label', '대답 생성 중지');
  document.body.dataset.submitted = 'true';
  if (mode !== 'pending') requestAnimationFrame(() => requestAnimationFrame(finish));
}
</script></body></html>"""


@dataclass(frozen=True, slots=True)
class BrowserFixture:
    cdp_url: str
    app_url: str
    context: BrowserContext
    video: bytes


def fixture_app(video: bytes) -> FastAPI:
    app = FastAPI()

    @app.get("/clip.mp4")
    async def clip() -> Response:
        return Response(video, media_type="video/mp4")

    @app.get("/app", response_class=HTMLResponse)
    async def surface(mode: str = "video") -> str:
        if mode == "auth":
            return '<div role="dialog">계속하려면 로그인하세요</div>'
        return HTML.replace("MODE", mode)

    return app


@asynccontextmanager
async def serve(app: FastAPI) -> AsyncIterator[str]:
    with socket() as sock:
        sock.bind(("127.0.0.1", 0))
        sock.listen(16)
        server = uvicorn.Server(uvicorn.Config(app, log_level="error", lifespan="off"))

        async def run() -> None:
            await server.serve(sockets=[sock])

        async with anyio.create_task_group() as group:
            group.start_soon(run)
            while not server.started:
                await anyio.sleep(0.02)
            try:
                yield f"http://127.0.0.1:{sock.getsockname()[1]}"
            finally:
                server.should_exit = True


async def _devtools_endpoint(stream: TextReceiveStream) -> str:
    async for chunk in stream:
        for line in chunk.splitlines():
            if DEVTOOLS_MARKER in line:
                return line.split(DEVTOOLS_MARKER, 1)[1].strip()
    return ""


async def _render_video(tmp_path: Path) -> bytes:
    target = tmp_path / "fixture.mp4"
    _ = await anyio.run_process([
        FFMPEG, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=32x32:d=0.1",
        "-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart", str(target),
    ])
    return await anyio.Path(target).read_bytes()


@asynccontextmanager
async def local_browser(tmp_path: Path) -> AsyncIterator[BrowserFixture]:
    video = await _render_video(tmp_path)
    async with serve(fixture_app(video)) as app_url, async_playwright() as driver:
        process = await anyio.open_process([
            CHROME, "--headless=new", "--no-sandbox", "--disable-dev-shm-usage",
            "--no-first-run", "--disable-background-networking",
            "--remote-debugging-port=0", f"--user-data-dir={tmp_path / 'chrome'}", "about:blank",
        ], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        async with process:
            assert process.stderr is not None
            endpoint = await _devtools_endpoint(TextReceiveStream(process.stderr))
            assert endpoint
            browser = await driver.chromium.connect_over_cdp(endpoint, no_defaults=True)
            try:
                yield BrowserFixture(endpoint, f"{app_url}/app", browser.contexts[0], video)
            finally:
                process.terminate()
