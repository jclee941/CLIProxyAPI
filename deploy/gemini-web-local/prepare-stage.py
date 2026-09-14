# /// script
# requires-python = ">=3.11"
# dependencies = ["PyYAML>=6,<7"]
# ///
"""Run on .114 with its existing Python/PyYAML: python3 prepare-stage.py."""

import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
from typing import TYPE_CHECKING, Final, cast

import yaml

if TYPE_CHECKING:
    from .stage_parsing import Original, decode, is_auth_record, is_config, is_containers, is_mapping, narrow
else:
    from stage_parsing import Original, decode, is_auth_record, is_config, is_containers, is_mapping, narrow

ROOT: Final = Path("/root/.local/state/gemini-web-importer")
BOOTSTRAP: Final = Path("/etc/cliproxy/gemini-web-importer")
IMAGE: Final = "sha256:8e03771cd4846b3f3547ef8b2d60a58f64e80754fccd0f18850da5c649fba323"
CORE_HASH: Final = "1cd125f9e1c969c157b77f010e391a17f269bc8fa7a30decf4c9f3336b401a1e"
PLUGIN_HASH: Final = "c6111b408c34a00b246108d33f69e92bff58d35c9144fcd95d438e29a38470aa"


def prepare() -> None:
    _ = os.umask(0o077)
    containers = decode(subprocess.check_output(["docker", "inspect", "cliproxyapi"]), is_containers)
    core = containers[0]
    assert core["Image"] == IMAGE
    executable = Path(f"/proc/{core['State']['Pid']}/root/CLIProxyAPI/CLIProxyAPI")
    assert hashlib.sha256(executable.read_bytes()).hexdigest() == "163fe79fdd84e7b071520212382d798a2163edfe3b85183312a1d17bd7dcc4a7"
    assert not ROOT.exists() and not BOOTSTRAP.exists()
    source = Path("/root/.local/state/gemini-web-local-cutover-20260914/qa")
    assert hashlib.sha256((source / "core").read_bytes()).hexdigest() == CORE_HASH
    assert hashlib.sha256((source / "plugins/gemini-web.so").read_bytes()).hexdigest() == PLUGIN_HASH
    config = narrow(cast(object, yaml.safe_load(Path("/run/cliproxy/config.yaml").read_text())), is_config)
    bindings = config["plugins"]["configs"]["gemini-web"]["maintenance_sources"]
    originals: dict[str, Original] = {}
    hashes: dict[str, str] = {}
    for path in sorted(Path("/opt/dashboard/auths").glob("*.json")):
        raw = path.read_bytes()
        hashes[path.name] = hashlib.sha256(raw).hexdigest()
        document = decode(raw, is_mapping)
        if document.get("type") == "gemini-web":
            record = narrow(document, is_auth_record)
            assert record["id"] == path.name
            binding = bindings[record["id"]]
            assert binding["token_ref"] == record["token_ref"]
            assert type(record.get("disabled", False)) is bool
            originals[path.name] = {"record": record, "binding": binding, "disabled": record.get("disabled", False)}
    assert len(originals) == len(bindings) == 5
    ROOT.mkdir(mode=0o700)
    BOOTSTRAP.mkdir(mode=0o700)
    for name in ("auths", "sessions", "plugins", "originals", "intake", "receipts", "empty-bin"):
        (ROOT / name).mkdir(mode=0o700)
    _ = (BOOTSTRAP / "session.key").write_text(base64.b64encode(secrets.token_bytes(32)).decode() + "\n")
    _ = (BOOTSTRAP / "management.key").write_text(secrets.token_urlsafe(48) + "\n")
    _ = (BOOTSTRAP / "api.key").write_text(secrets.token_urlsafe(48) + "\n")
    _ = (ROOT / "originals/mappings.json").write_text(json.dumps(originals))
    _ = (ROOT / "originals/docker-inspect.json").write_text(json.dumps(core))
    _ = (ROOT / "originals/auth-hashes.json").write_text(json.dumps(hashes))
    _ = shutil.copyfile(executable, ROOT / "originals/CLIProxyAPI")
    for path in (Path("/run/cliproxy/config.yaml"), Path("/opt/dashboard/config.local.yaml"), Path("/opt/dashboard/docker-compose.dashboard-only.yml")):
        _ = shutil.copyfile(path, ROOT / "originals" / ("runtime-config.yaml" if str(path).startswith("/run/") else path.name))
    for name, original in originals.items():
        assert "record" in original
        _ = shutil.copyfile(Path("/opt/dashboard/auths") / name, ROOT / "originals" / name)
        _ = (ROOT / "auths" / name).write_text(json.dumps({**original["record"], "disabled": True}))
    _ = shutil.copyfile(source / "core", ROOT / "core")
    (ROOT / "core").chmod(0o700)
    _ = shutil.copyfile(source / "plugins/gemini-web.so", ROOT / "plugins/gemini-web.so")
    for name in ("start-stage.sh", "import-stage.py", "stage_parsing.py"):
        _ = shutil.copyfile(Path(__file__).parent / name, ROOT / name)
    stage_config = {
        "host": "0.0.0.0", "port": 8317, "auth-dir": "/stage/auths",
        "remote-management": {"allow-remote": True, "secret-key": "", "disable-control-panel": True, "disable-auto-update-panel": True},
        "api-keys": [(BOOTSTRAP / "api.key").read_text().strip()],
        "commercial-mode": True, "request-log": False, "request-retry": 0,
        "logging-to-file": False,
        "plugins": {"enabled": True, "dir": "/stage/plugins", "configs": {"gemini-web": {
            "enabled": True, "vault": "homelab", "maintenance_sources": bindings,
            "session_dir": "/stage/sessions", "manager_origin": "https://cliproxy.jclee.me",
            "browser_extension_id": "jkomnpgdokmnojgbapacnnkakgnbekjn",
        }}},
        "oauth-model-alias": {"gemini-web": [{"name": "gemini-web-flash-3.8", "alias": "gemini-web-flash", "fork": True}]},
    }
    _ = (ROOT / "config.yaml").write_text(json.dumps(stage_config))
    command = [
        "docker", "run", "--detach", "--pull", "never", "--name", "gemini-web-importer",
        "--restart", "unless-stopped", "--network", "cliproxyapi_backend",
        "--publish", "127.0.0.1:18319:8317", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--read-only", "--log-driver", "none",
        "--mount", f"type=bind,src={ROOT / 'core'},dst=/stage/core,readonly",
        "--mount", f"type=bind,src={ROOT / 'config.yaml'},dst=/stage/config.yaml,readonly",
        "--mount", f"type=bind,src={ROOT / 'start-stage.sh'},dst=/stage/start-stage.sh,readonly",
        "--mount", f"type=bind,src={ROOT / 'plugins'},dst=/stage/plugins,readonly",
        "--mount", f"type=bind,src={ROOT / 'empty-bin'},dst=/stage/empty-bin,readonly",
        "--mount", f"type=bind,src={ROOT / 'auths'},dst=/stage/auths",
        "--mount", f"type=bind,src={ROOT / 'sessions'},dst=/stage/sessions",
        "--mount", f"type=bind,src={BOOTSTRAP},dst=/bootstrap,readonly",
        "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--workdir", "/tmp",
        "--entrypoint", "/bin/sh", IMAGE, "/stage/start-stage.sh",
    ]
    _ = (ROOT / "recreate-command.json").write_text(json.dumps(command))
    _ = subprocess.run(command, check=True, stdout=subprocess.DEVNULL)
    print("stage_created; readiness_not_yet_verified")


if __name__ == "__main__":
    prepare()
