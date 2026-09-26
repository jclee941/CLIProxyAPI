# /// script
# requires-python = ">=3.11"
# dependencies = ["PyYAML>=6,<7"]
# ///
import base64
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import TYPE_CHECKING, cast

import yaml

if TYPE_CHECKING:
    from .stage_parsing import decode, is_mapping, is_sequence, is_text, narrow
else:
    from stage_parsing import decode, is_mapping, is_sequence, is_text, narrow


def generate(model: str) -> None:
    assert model in ("gemini-web-flash", "gemini-web-omni")
    root = Path("/root/.local/state/gemini-web-importer")
    receipt = root / ("production-" + model + ".json")
    with receipt.open("x") as stream:
        _ = stream.write(json.dumps({"model": model, "status": "submitting", "submissions": 1}))
    config = narrow(cast(object, yaml.safe_load(Path("/opt/dashboard/gemini-web/config.yaml").read_text())), is_mapping)
    key = narrow(narrow(config["api-keys"], is_sequence)[0], is_text)
    prompt = "Reply with exactly OK." if model == "gemini-web-flash" else "Create a short video of a blue ball resting on a white table. Static camera."
    payload = {"contents": [{"role": "user", "parts": [{"text": prompt}]}]}
    curl = "\n".join([
        "silent", 'proxy = ""',
        "url = " + json.dumps("http://127.0.0.1:18318/v1beta/models/" + model + ":generateContent"),
        'request = "POST"', 'header = "Content-Type: application/json"',
        "header = " + json.dumps("x-goog-api-key: " + key),
        "data = " + json.dumps(json.dumps(payload)), 'write-out = "\\n%{http_code}"',
    ])
    response = subprocess.run(["curl", "--config", "-"], input=curl, text=True, capture_output=True)
    if response.returncode:
        _ = receipt.write_text(json.dumps({"model": model, "status": "outcome_unknown", "submissions": 1}))
        raise SystemExit("outcome_unknown; do not resubmit")
    raw, status = response.stdout.rsplit("\n", 1)
    body = decode(raw, is_mapping)
    if status != "200":
        message = "upstream_rejected"
        error = body.get("error")
        if is_mapping(error) and is_text(error.get("message")):
            candidate = narrow(error["message"], is_text)
            if len(candidate) < 200 and all(character.isalnum() or character in "_:- ." for character in candidate):
                message = candidate
        _ = receipt.write_text(json.dumps({"model": model, "status": "rejected", "http": int(status), "error": message, "submissions": 1}))
        print("generation_rejected; inspect sanitized receipt")
        return
    texts: list[str] = []
    videos: list[bytes] = []
    for candidate in narrow(body["candidates"], is_sequence):
        content = narrow(narrow(candidate, is_mapping)["content"], is_mapping)
        for part in narrow(content["parts"], is_sequence):
            parsed = narrow(part, is_mapping)
            if "text" in parsed:
                texts.append(narrow(parsed["text"], is_text))
            if "inlineData" in parsed:
                inline = narrow(parsed["inlineData"], is_mapping)
                assert inline["mimeType"] == "video/mp4"
                videos.append(base64.b64decode(narrow(inline["data"], is_text), validate=True))
    if model == "gemini-web-flash":
        assert any(text.strip() for text in texts)
        _ = receipt.write_text(json.dumps({"model": model, "status": "succeeded", "http": 200, "nonempty_text": True, "exact_ok": "".join(texts).strip() == "OK", "submissions": 1}))
    else:
        assert len(videos) == 1 and len(videos[0]) > 12 and videos[0][4:8] == b"ftyp"
        artifact = root / "production-omni.mp4"
        _ = artifact.write_bytes(videos[0])
        _ = receipt.write_text(json.dumps({"model": model, "status": "succeeded", "http": 200, "video_bytes": len(videos[0]), "mp4_signature": True, "submissions": 1}))
    print("generation_succeeded; sanitized receipt saved")


if __name__ == "__main__":
    _ = os.umask(0o077)
    generate(sys.argv[1])
