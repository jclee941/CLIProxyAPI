import dataclasses
import hashlib
import http.server
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import threading
import unittest
from typing import Any

HERE = pathlib.Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("cpa_converge", HERE / "cpa-converge.py")
assert SPEC is not None and SPEC.loader is not None
converge_module = importlib.util.module_from_spec(SPEC)
sys.modules["cpa_converge"] = converge_module
SPEC.loader.exec_module(converge_module)

FAKE_DOCKER = r"""#!/usr/bin/env python3
import json, os, pathlib, sys
state = pathlib.Path(os.environ["FAKE_STATE"])
with (state / "docker.log").open("a") as log:
    log.write(" ".join(sys.argv[1:]) + "\n")
args = sys.argv[1:]
if args[0] == "exec" and args[2] == "date":
    print(os.environ["FAKE_NOW"])
elif args[0] == "exec":
    print((state / "main.log").read_text(), end="")
elif args[0] in ("start", "restart") and not (state / "broken").exists():
    plugins = pathlib.Path(os.environ["FAKE_PLUGINS"])
    loaded = json.loads((state / "loaded.json").read_text())
    for plugin_id in json.loads((state / "restart_ids.json").read_text()):
        names = sorted(p.name for p in plugins.iterdir() if p.name.startswith(plugin_id + "-v") and p.name.endswith(".so"))
        loaded[plugin_id] = names[0] if len(names) == 1 else ""
    (state / "loaded.json").write_text(json.dumps(loaded))
    (state / "core_commit").write_text(pathlib.Path(os.environ["FAKE_CORE"]).read_bytes().split()[1].decode())
"""

PICK = "[2026-09-26 {}] [{}] [debug] [conductor_execution.go:1170] Use OAuth provider=gemini-web auth_file=gemini-web-a.json for model gemini-omni-1.1-flash via proxy\n"
ANSWER = '[2026-09-26 {}] [{}] [info ] [gin_logger.go:97] 200 | 60s | 1.2.3.4 | POST "/v1beta/interactions"\n'


def management_handler(state: pathlib.Path, plugins: pathlib.Path) -> type[http.server.BaseHTTPRequestHandler]:
    def loadable_name(plugin_id: str) -> str:
        names = sorted(p.name for p in plugins.iterdir() if p.name.startswith(plugin_id + "-v") and p.name.endswith(".so"))
        return names[0] if len(names) == 1 else ""

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, format: str, *args: Any) -> None:
            return

        def reply(self, body: object) -> None:
            raw = json.dumps(body).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("X-CPA-COMMIT", (state / "core_commit").read_text())
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self) -> None:
            release = state / "releases" / self.path.removeprefix("/releases/")
            if self.path.startswith("/releases/") and release.is_file():
                raw = release.read_bytes()
                self.send_response(200)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
            elif self.path == "/v0/management/plugins":
                loaded = json.loads((state / "loaded.json").read_text())
                self.reply({"plugins": [{"id": i, "path": "/CLIProxyAPI/plugins/" + n, "registered": bool(n)} for i, n in loaded.items()]})
            elif self.path == "/v0/management/plugins/gemini-web/accounts":
                self.reply({"accounts": json.loads((state / "accounts.json").read_text())})
            else:
                self.send_error(404)

        def do_PATCH(self) -> None:
            plugin_id = self.path.split("/")[4]
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with (state / "patches.log").open("a") as log:
                log.write(f"{plugin_id} {body}\n")
            loaded = json.loads((state / "loaded.json").read_text())
            loaded[plugin_id] = loadable_name(plugin_id)
            (state / "loaded.json").write_text(json.dumps(loaded))
            self.reply({"status": "ok"})

    return Handler


@dataclasses.dataclass
class Host:
    plugins: pathlib.Path
    state: pathlib.Path
    commit: str
    core: bytes
    settings: Any

    def live(self, plugin_id: str, content: bytes) -> None:
        (self.plugins / f"{plugin_id}-v0.1.0.1.so").write_bytes(content)

    def live_core(self, content: bytes) -> None:
        self.settings.core_binary.write_bytes(content)
        (self.state / "core_commit").write_text(content.split()[1].decode())

    def core_files(self) -> list[str]:
        return sorted(path.name for path in self.settings.core_binary.parent.glob("CLIProxyAPI*"))

    def accounts(self, *statuses: str) -> None:
        (self.state / "accounts.json").write_text(json.dumps([{"status": status} for status in statuses]))

    def lifecycle(self) -> list[str]:
        log = self.state / "docker.log"
        calls = log.read_text().splitlines() if log.exists() else []
        return [call.split()[0] for call in calls if call.split()[0] in ("stop", "start", "restart")]

    def docker_calls(self) -> list[str]:
        log = self.state / "docker.log"
        return log.read_text().splitlines() if log.exists() else []


def host(test: unittest.TestCase) -> Host:
    root = pathlib.Path(tempfile.mkdtemp())
    clone, plugins, state, binaries = (root / name for name in ("clone", "plugins", "state", "bin"))
    for directory in (clone / "deploy/gw", clone / "deploy/cw", clone / "core", plugins / "gw", state / "releases", binaries):
        directory.mkdir(parents=True)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), management_handler(state, plugins))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    test.addCleanup(server.server_close)
    test.addCleanup(server.shutdown)
    api = f"http://127.0.0.1:{server.server_address[1]}"
    manifest = {
        "core": {"paths": ["core"], "release": f"{api}/releases", "asset": "CLIProxyAPI"},
        "plugins": [
            {"id": "gw", "artifact": "deploy/gw/gw.so", "reload": "restart"},
            {"id": "cw", "artifact": "deploy/cw/cw.so", "reload": "hot"},
        ],
        "files": [{"artifact": "deploy/gw/index.html", "target": "gw/index.html"}],
    }
    (clone / "deploy/plugins.json").write_text(json.dumps(manifest))
    (clone / "deploy/gw/gw.so").write_bytes(b"gw-new")
    (clone / "deploy/cw/cw.so").write_bytes(b"cw-new")
    (clone / "deploy/gw/index.html").write_text("dashboard-new")
    (clone / "core/main.go").write_text("package main\n")
    git = ["git", "-C", str(clone), "-c", "user.name=t", "-c", "user.email=t@t"]
    subprocess.run(git[:3] + ["init", "-q"], check=True)
    subprocess.run(git + ["add", "."], check=True)
    subprocess.run(git + ["commit", "-q", "-m", "desired"], check=True)
    commit = subprocess.run(git[:3] + ["rev-parse", "--short=7", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()

    core = f"core {commit}".encode()
    (state / "releases" / f"CLIProxyAPI-{commit}").write_bytes(core)
    (state / "releases" / f"CLIProxyAPI-{commit}.sha256").write_text(f"{hashlib.sha256(core).hexdigest()}  CLIProxyAPI-{commit}\n")
    (root / "CLIProxyAPI").write_bytes(core)
    (state / "core_commit").write_text(commit)
    (plugins / "gw-v0.1.0.1.so").write_bytes(b"gw-new")
    (plugins / "cw-v0.1.0.1.so").write_bytes(b"cw-new")
    (plugins / "gw/index.html").write_text("dashboard-new")
    (state / "loaded.json").write_text(json.dumps({"gw": "gw-v0.1.0.1.so", "cw": "cw-v0.1.0.1.so"}))
    (state / "restart_ids.json").write_text(json.dumps(["gw"]))
    (state / "main.log").write_text("")
    (root / "core.env").write_text("MANAGEMENT_PASSWORD='key'\n")
    docker = binaries / "docker"
    docker.write_text(FAKE_DOCKER)
    docker.chmod(0o755)

    saved = dict(os.environ)
    test.addCleanup(lambda: (os.environ.clear(), os.environ.update(saved)))
    os.environ.update(
        PATH=f"{binaries}:{os.environ['PATH']}",
        FAKE_STATE=str(state),
        FAKE_PLUGINS=str(plugins),
        FAKE_CORE=str(root / "CLIProxyAPI"),
        FAKE_NOW="2026-09-26 02:00:00",
    )

    settings = converge_module.Settings(
        clone=clone,
        manifest=clone / "deploy/plugins.json",
        plugin_dir=plugins,
        core_binary=root / "CLIProxyAPI",
        container="cpa",
        container_plugin_dir="/CLIProxyAPI/plugins",
        api=api,
        core_env=root / "core.env",
        idle_confirm=0,
        health_wait=0.3,
        poll=0.01,
        dry_run=False,
    )
    fixture = Host(plugins, state, commit, core, settings)
    fixture.accounts("ready", "ready")
    return fixture


class ConvergeTest(unittest.TestCase):
    def test_in_sync_touches_nothing(self) -> None:
        fixture = host(self)

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.docker_calls(), [])
        self.assertEqual(sorted(p.name for p in fixture.plugins.iterdir()), ["cw-v0.1.0.1.so", "gw", "gw-v0.1.0.1.so"])

    def test_a_dashboard_and_a_hot_plugin_converge_without_a_restart(self) -> None:
        fixture = host(self)
        (fixture.plugins / "gw/index.html").write_text("dashboard-old")
        fixture.live("cw", b"cw-old")

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual((fixture.plugins / "gw/index.html").read_text(), "dashboard-new")
        installed = converge_module.loadable(fixture.plugins, "cw")
        self.assertEqual([path.read_bytes() for path in installed], [b"cw-new"])
        self.assertTrue(installed[0].name.endswith(f".g{fixture.commit}.so"))
        self.assertEqual((fixture.plugins / f"cw-v0.1.0.1.so.prev-g{fixture.commit}").read_bytes(), b"cw-old")
        self.assertIn("cw {'enabled': True}", (fixture.state / "patches.log").read_text())
        self.assertEqual(fixture.lifecycle(), [])

    def test_a_restart_waits_while_an_account_generates(self) -> None:
        fixture = host(self)
        fixture.live("gw", b"gw-old")
        fixture.accounts("ready", "generating")

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.lifecycle(), [])
        self.assertEqual((fixture.plugins / "gw-v0.1.0.1.so").read_bytes(), b"gw-old")

    def test_a_restart_waits_for_a_picked_turn_that_has_not_answered(self) -> None:
        fixture = host(self)
        fixture.live("gw", b"gw-old")
        (fixture.state / "main.log").write_text(PICK.format("01:55:00", "0123abcd") + PICK.format("01:40:00", "89abcdef"))

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.lifecycle(), [])

    def test_a_restart_plugin_is_swapped_under_a_stopped_container_when_idle(self) -> None:
        fixture = host(self)
        fixture.live("gw", b"gw-old")
        (fixture.state / "main.log").write_text(PICK.format("01:55:00", "0123abcd") + ANSWER.format("01:56:00", "0123abcd"))

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.lifecycle(), ["stop", "start"])
        self.assertEqual([path.read_bytes() for path in converge_module.loadable(fixture.plugins, "gw")], [b"gw-new"])
        self.assertEqual((fixture.plugins / f"gw-v0.1.0.1.so.prev-g{fixture.commit}").read_bytes(), b"gw-old")

    def test_a_host_that_does_not_come_back_gets_one_restart_and_no_rollback(self) -> None:
        fixture = host(self)
        fixture.live("gw", b"gw-old")
        (fixture.state / "broken").write_text("")

        with self.assertRaises(converge_module.ConvergeError):
            converge_module.converge(fixture.settings)

        self.assertEqual(fixture.lifecycle(), ["stop", "start", "restart"])
        self.assertEqual([path.read_bytes() for path in converge_module.loadable(fixture.plugins, "gw")], [b"gw-new"])

    def test_a_published_core_is_swapped_under_a_stopped_container_when_idle(self) -> None:
        fixture = host(self)
        fixture.live_core(b"core old")

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.lifecycle(), ["stop", "start"])
        self.assertEqual(fixture.settings.core_binary.read_bytes(), fixture.core)
        self.assertEqual(fixture.settings.core_binary.with_name("CLIProxyAPI.prev").read_bytes(), b"core old")
        self.assertEqual((fixture.state / "core_commit").read_text(), fixture.commit)
        self.assertEqual(fixture.core_files(), ["CLIProxyAPI", "CLIProxyAPI.prev"])

    def test_a_core_and_a_restart_plugin_share_one_restart(self) -> None:
        fixture = host(self)
        fixture.live_core(b"core old")
        fixture.live("gw", b"gw-old")

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.lifecycle(), ["stop", "start"])
        self.assertEqual(fixture.settings.core_binary.read_bytes(), fixture.core)
        self.assertEqual([path.read_bytes() for path in converge_module.loadable(fixture.plugins, "gw")], [b"gw-new"])

    def test_a_core_swap_waits_while_an_account_generates(self) -> None:
        fixture = host(self)
        fixture.live_core(b"core old")
        fixture.accounts("ready", "generating")

        self.assertEqual(converge_module.converge(fixture.settings), 0)

        self.assertEqual(fixture.lifecycle(), [])
        self.assertEqual(fixture.settings.core_binary.read_bytes(), b"core old")
        self.assertEqual(fixture.core_files(), ["CLIProxyAPI"])

    def test_an_unpublished_core_fails_before_touching_the_host(self) -> None:
        fixture = host(self)
        fixture.live_core(b"core old")
        for published in (fixture.state / "releases").iterdir():
            published.unlink()

        with self.assertRaises(converge_module.ConvergeError):
            converge_module.converge(fixture.settings)

        self.assertEqual(fixture.docker_calls(), [])
        self.assertEqual(fixture.settings.core_binary.read_bytes(), b"core old")

    def test_a_core_download_that_does_not_match_its_published_sha256_is_refused(self) -> None:
        fixture = host(self)
        fixture.live_core(b"core old")
        (fixture.state / "releases" / f"CLIProxyAPI-{fixture.commit}").write_bytes(b"core tampered")

        with self.assertRaises(converge_module.ConvergeError):
            converge_module.converge(fixture.settings)

        self.assertEqual(fixture.lifecycle(), [])
        self.assertEqual(fixture.core_files(), ["CLIProxyAPI"])
        self.assertEqual(fixture.settings.core_binary.read_bytes(), b"core old")

    def test_a_core_the_host_does_not_report_gets_one_restart_and_no_rollback(self) -> None:
        fixture = host(self)
        fixture.live_core(b"core old")
        (fixture.state / "broken").write_text("")

        with self.assertRaises(converge_module.ConvergeError):
            converge_module.converge(fixture.settings)

        self.assertEqual(fixture.lifecycle(), ["stop", "start", "restart"])
        self.assertEqual(fixture.settings.core_binary.read_bytes(), fixture.core)


if __name__ == "__main__":
    unittest.main()
