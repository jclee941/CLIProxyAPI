#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = ["openai==2.26.0"]
# ///
# Run with uv run python-client.py BASE_URL SCENARIO ACK_URL inside the fixture.
from __future__ import annotations

import json
import sys
import httpx
from openai import APIError, APIStatusError, OpenAI


def main() -> None:
    base_url, scenario, ack_url = sys.argv[1:]
    streaming = scenario in {
        "http-stream-terminal", "sse-first", "sse-role", "sse-partial", "success-stream"
    }
    content = ""
    stops = 0
    failed = False
    status: int | None = None
    retry_header: str | None = None
    code: str | None = None
    with OpenAI(api_key="synthetic-downstream", base_url=base_url) as client:
        try:
            if streaming:
                with client.chat.completions.create(
                    model="synthetic-pro", messages=[{"role": "user", "content": "synthetic"}], stream=True
                ) as stream:
                    for chunk in stream:
                        for choice in chunk.choices:
                            content += choice.delta.content or ""
                            stops += int(choice.finish_reason == "stop")
                        if scenario == "sse-partial" and content:
                            with httpx.Client() as acknowledgment:
                                assert acknowledgment.get(ack_url).status_code == 200
            else:
                response = client.chat.completions.create(
                    model="synthetic-pro", messages=[{"role": "user", "content": "synthetic"}]
                )
                content = response.choices[0].message.content or ""
                stops = int(response.choices[0].finish_reason == "stop")
        except APIError as error:
            failed = True
            code = error.code
            if isinstance(error, APIStatusError):
                status = error.response.status_code
                retry_header = error.response.headers.get("x-should-retry", "") or None
    print(json.dumps({"failed": failed, "code": code, "status": status, "retryHeader": retry_header, "content": content, "stops": stops}))


if __name__ == "__main__":
    main()
