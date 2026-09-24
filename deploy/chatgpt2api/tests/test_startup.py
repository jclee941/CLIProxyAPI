"""Exercise retained launchers without credentials, a vault, or a Docker daemon."""

from __future__ import annotations

import json
import os
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import TypedDict, cast, final, override

DEPLOY = Path(__file__).resolve().parents[2]
SCRIPTS = {
    "gemini-web-native": DEPLOY / "gemini-web-native/start.sh",
    "gemini-web2api": DEPLOY / "gemini-web2api/start.sh",
}
INPUTS = {
    "gemini-web-native": {"gemini-web-native/config.json": "config.json"},
    "gemini-web2api": {
        "gemini-web2api/config.json": "config.json",
        "gemini-web2api/cookie.txt": "cookie.txt",
    },
}
COMPOSE = {
    "gemini-web-native": [
        "compose", "-f", "/opt/gemini-web-native/docker-compose.yml",
        "up", "-d", "--force-recreate", "gemini-web-native",
    ],
    "gemini-web2api": [
        "compose", "-f", "/opt/gemini-web2api/docker-compose.yml",
        "up", "-d", "--force-recreate", "gemini-web2api",
    ],
}


class DockerInvocation(TypedDict):
    argv: list[str]
    files: dict[str, str]


@final
class StartupTests(unittest.TestCase):
    def __init__(self, methodName: str = "runTest") -> None:
        super().__init__(methodName)
        temporary = tempfile.TemporaryDirectory(prefix="cliproxy startup ")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.config = self.root / "etc"
        self.runtime = {name: self.root / "run" / name for name in SCRIPTS}
        self.bin = self.root / "bin"
        self.docker_log = self.root / "docker.jsonl"
        self.install_log = self.root / "install.jsonl"
        self.op_called = self.root / "op-called"
        self.shell_called = self.root / "shell-called"
        self.env = {
            "PATH": f"{self.bin}:/usr/bin:/bin",
            "CLIPROXY_CONFIG_DIR": str(self.config),
            "GEMINI_WEB_NATIVE_RUN_DIR": str(self.runtime["gemini-web-native"]),
            "GEMINI_WEB2API_RUN_DIR": str(self.runtime["gemini-web2api"]),
            "TEST_DOCKER_LOG": str(self.docker_log),
            "TEST_INSTALL_LOG": str(self.install_log),
            "TEST_OP_CALLED": str(self.op_called),
        }

    @override
    def setUp(self) -> None:
        self.bin.mkdir()
        values = {
            "core/config.yaml": 'proxy-url: "http://synthetic-proxy:3128"\n',
            "chatgpt2api/config.json": json.dumps({
                "proxy": "replaced-only-in-runtime", "auth-key": "synthetic-secret",
                "proxy_runtime": {"enabled": True},
            }),
            "chatgpt2api/chatgpt2api.env": (
                f'CHATGPT2API_AUTH_KEY=synthetic-secret$(touch "{self.shell_called}")\n'
            ),
            "gemini-web-native/config.json": '{"api_key": "synthetic-native-key"}\n',
            "gemini-web2api/config.json": json.dumps({
                "api_keys": ["synthetic-web2api-key"], "xsrf_token": "synthetic-xsrf",
                "cookie_file": "/run/gemini-web2api/cookie.txt",
            }),
            "gemini-web2api/cookie.txt": "SID=synthetic-cookie; Other=synthetic-extra\n",
        }
        for relative, value in values.items():
            source = self.config / relative
            source.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            _ = source.write_text(value, encoding="utf-8")
            source.chmod(0o600)
        self.write_command("op", """
import os
from pathlib import Path
Path(os.environ["TEST_OP_CALLED"]).touch()
raise SystemExit(97)
""")
        self.write_command("docker", """
import json
import os
import sys
from pathlib import Path
runtime = Path(os.environ["TEST_RUNTIME_DIR"])
snapshot = {p.name: p.read_text() for p in runtime.iterdir() if p.is_file()}
with open(os.environ["TEST_DOCKER_LOG"], "a") as log:
    log.write(json.dumps({"argv": sys.argv[1:], "files": snapshot}) + "\\n")
raise SystemExit(int(os.environ.get("TEST_DOCKER_STATUS", "0")))
""")
        # Keep real install/copy/mode behavior. Rootless runs cannot chown but
        # record those exact arguments; root runs also exercise real ownership.
        self.write_command("install", """
import json
import os
import sys
args = sys.argv[1:]
with open(os.environ["TEST_INSTALL_LOG"], "a") as log:
    log.write(json.dumps(args) + "\\n")
if os.geteuid() != 0:
    unprivileged = []
    iterator = iter(args)
    for arg in iterator:
        if arg in ("-o", "-g"):
            next(iterator)
        else:
            unprivileged.append(arg)
    args = unprivileged
os.execv("/usr/bin/install", ["install", *args])
""")

    def write_command(self, name: str, body: str) -> None:
        path = self.bin / name
        _ = path.write_text("#!/usr/bin/python3\n" + body, encoding="utf-8")
        path.chmod(0o700)

    def run_launcher(self, name: str, *args: str) -> subprocess.CompletedProcess[str]:
        self.docker_log.unlink(missing_ok=True)
        sources_before = {
            path: path.read_bytes() for path in self.config.rglob("*") if path.is_file()
        }
        result = subprocess.run(
            ["/bin/sh", str(SCRIPTS[name]), *args],
            env={**self.env, "TEST_RUNTIME_DIR": str(self.runtime[name])},
            text=True, capture_output=True, timeout=10, check=False,
        )
        self.assertFalse(self.op_called.exists(), "op must never be called")
        self.assertFalse(self.shell_called.exists(), "local files must not be sourced")
        self.assertNotIn("synthetic-secret", result.stdout + result.stderr)
        for path, content in sources_before.items():
            self.assertEqual(path.read_bytes(), content, "source must remain unchanged")
        return result

    def assert_permissions(self, path: Path, mode: int, uid: int, gid: int) -> None:
        info = path.stat()
        self.assertEqual(stat.S_IMODE(info.st_mode), mode, str(path))
        if os.geteuid() == 0:
            self.assertEqual((info.st_uid, info.st_gid), (uid, gid), str(path))

    def assert_docker(self, name: str, *args: str) -> dict[str, str]:
        invocations = self.docker_log.read_text().splitlines()
        self.assertEqual(len(invocations), 1, "Docker must be invoked exactly once")
        invocation = cast(DockerInvocation, json.loads(invocations[0]))
        self.assertEqual(invocation["argv"], [*COMPOSE[name], *args])
        self.assertEqual(
            invocation["files"],
            {p.name: p.read_text() for p in self.runtime[name].iterdir() if p.is_file()},
            "Docker must see the complete runtime files, not old or staged inputs",
        )
        return invocation["files"]

    def assert_gemini_installs(self, name: str) -> None:
        runtime = self.runtime[name]
        expected = [["-d", "-m", "750", "-o", "root", "-g", "65532", str(runtime)]]
        if name == "gemini-web-native":
            expected.append([
                "-d", "-m", "700", "-o", "65532", "-g", "65532",
                str(runtime / "cookies"),
            ])
        for source, target in INPUTS[name].items():
            suffix = ".next" if name == "gemini-web2api" else ""
            expected.append([
                "-m", "640", "-o", "root", "-g", "65532", str(self.config / source),
                str(runtime / (target + suffix)),
            ])
        self.assertEqual(
            [json.loads(line) for line in self.install_log.read_text().splitlines()], expected,
        )

    def test_retired_chatgpt_launcher_refuses_to_start_services(self) -> None:
        result = subprocess.run(
            ["/bin/sh", str(DEPLOY / "chatgpt2api/cliproxy-compose"), "up", "-d"],
            env=self.env, capture_output=True, text=True, check=False,
        )
        self.assertEqual(result.returncode, 2)
        self.assertFalse(self.docker_log.exists())
        self.assertFalse(self.op_called.exists())

    def test_native_copies_config_and_preserves_cookie_directory(self) -> None:
        runtime = self.runtime["gemini-web-native"]
        cookies = runtime / "cookies"
        cookies.mkdir(parents=True)
        _ = (cookies / "existing-cookie").write_text("synthetic-existing-cookie")
        result = self.run_launcher("gemini-web-native")
        self.assertEqual(result.returncode, 0, result.stderr)
        files = self.assert_docker("gemini-web-native")
        self.assertEqual(files, {
            "config.json": (self.config / "gemini-web-native/config.json").read_text(),
        })
        self.assertEqual((cookies / "existing-cookie").read_text(), "synthetic-existing-cookie")
        self.assert_permissions(runtime, 0o750, 0, 65532)
        self.assert_permissions(runtime / "config.json", 0o640, 0, 65532)
        self.assert_permissions(cookies, 0o700, 65532, 65532)
        self.assert_gemini_installs("gemini-web-native")

    def test_web2api_stages_both_files_before_docker(self) -> None:
        runtime = self.runtime["gemini-web2api"]
        runtime.mkdir(parents=True)
        for name in INPUTS["gemini-web2api"].values():
            _ = (runtime / name).write_text("synthetic-old")
        result = self.run_launcher("gemini-web2api")
        self.assertEqual(result.returncode, 0, result.stderr)
        files = self.assert_docker("gemini-web2api")
        self.assertEqual(files, {
            target: (self.config / source).read_text()
            for source, target in INPUTS["gemini-web2api"].items()
        })
        self.assert_permissions(runtime, 0o750, 0, 65532)
        for name in files:
            self.assert_permissions(runtime / name, 0o640, 0, 65532)
        self.assert_gemini_installs("gemini-web2api")

    def test_missing_empty_and_non_file_inputs_fail_before_copy_or_docker(self) -> None:
        for name, inputs in INPUTS.items():
            runtime = self.runtime[name]
            runtime.mkdir(parents=True)
            previous = {target: "synthetic-existing-runtime" for target in inputs.values()}
            for target, value in previous.items():
                _ = (runtime / target).write_text(value)
            for relative in inputs:
                source = self.config / relative
                original = source.read_bytes()
                for invalid in ("missing", "empty", "directory"):
                    with self.subTest(launcher=name, source=relative, invalid=invalid):
                        source.unlink()
                        if invalid == "empty":
                            source.touch()
                        elif invalid == "directory":
                            source.mkdir()
                        result = self.run_launcher(name)
                        self.assertNotEqual(result.returncode, 0)
                        self.assertFalse(self.docker_log.exists())
                        self.assertFalse(self.install_log.exists())
                        self.assertEqual(
                            {p.name: p.read_text() for p in runtime.iterdir()}, previous,
                        )
                        if invalid == "directory":
                            source.rmdir()
                        _ = source.write_bytes(original)

    def test_docker_failure_is_returned_without_retry(self) -> None:
        self.env["TEST_DOCKER_STATUS"] = "42"
        for name in SCRIPTS:
            with self.subTest(launcher=name):
                result = self.run_launcher(name)
                self.assertEqual(result.returncode, 42, result.stderr)
                _ = self.assert_docker(name)


if __name__ == "__main__":
    _ = unittest.main()
