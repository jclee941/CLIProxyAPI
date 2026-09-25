from contextlib import ExitStack
import os
from pathlib import Path
import stat
import subprocess
import tempfile
from typing import final
import unittest


ROOT = Path(__file__).resolve().parents[1]
KEYS = {
    "cpa_admin_key": b"fixture admin\n$'\"\\\x00\xff\n\n",
    "data.key": bytes(range(32)),
}
COMPOSE_ARGS = [
    "compose", "-f", "/opt/cpa-manager-plus/compose.yml",
    "--profile", "approval-required", "up", "-d", "--no-deps",
    "--force-recreate", "cpa-manager-plus",
]


@final
class StartupFixture:
    def __init__(self, directory: Path) -> None:
        self.directory = directory
        self.key_dir = directory / "durable keys"
        self.key_dir.mkdir(mode=0o700)
        self.runtime_dir = directory / "runtime keys"
        self.bin_dir = directory / "bin"
        self.bin_dir.mkdir()
        self.docker_calls = directory / "docker.calls"
        self.op_calls = directory / "op.calls"
        self.env = {
            "PATH": f"{self.bin_dir}:/usr/bin:/bin",
            "LC_ALL": "C",
            "CPA_MANAGER_KEY_DIR": str(self.key_dir),
            "CPA_MANAGER_RUNTIME_DIR": str(self.runtime_dir),
            "DOCKER_CALLS": str(self.docker_calls),
            "OP_CALLS": str(self.op_calls),
        }
        self.write_keys()
        self.write_executable("docker", r"""#!/bin/sh
set -eu
printf '%s\n' "$@" >> "$DOCKER_CALLS"
for key in cpa_admin_key data.key; do
    cmp -s "$CPA_MANAGER_KEY_DIR/$key" "$CPA_MANAGER_RUNTIME_DIR/$key"
    test ! -e "$CPA_MANAGER_RUNTIME_DIR/$key.next"
    test "$(stat -c %a "$CPA_MANAGER_RUNTIME_DIR/$key")" = 600
done
test "$(stat -c %a "$CPA_MANAGER_RUNTIME_DIR")" = 700
exit "${DOCKER_STATUS:-0}"
""")
        self.write_executable("op", """#!/bin/sh
set -eu
: > "$OP_CALLS"
exit 97
""")

    def write_executable(self, name: str, source: str) -> None:
        path = self.bin_dir / name
        _ = path.write_text(source)
        path.chmod(0o700)

    def write_keys(self) -> None:
        for name, content in KEYS.items():
            path = self.key_dir / name
            _ = path.write_bytes(content)
            path.chmod(0o600)


class StartupTests(unittest.TestCase):
    def fixture(self) -> StartupFixture:
        temporary = tempfile.TemporaryDirectory(prefix="cpa-manager-startup-")
        self.addCleanup(temporary.cleanup)
        return StartupFixture(Path(temporary.name))

    def start(self, fixture: StartupFixture) -> subprocess.CompletedProcess[bytes]:
        result = subprocess.run(
            ["/bin/sh", str(ROOT / "start.sh")],
            cwd=fixture.directory, env=fixture.env, capture_output=True, timeout=10,
        )
        self.assertFalse(fixture.op_calls.exists(), "startup must never invoke op")
        return result

    def assert_runtime_keys(self, fixture: StartupFixture) -> None:
        metadata = fixture.runtime_dir.stat()
        self.assertEqual(stat.S_IMODE(metadata.st_mode), 0o700)
        self.assertEqual((metadata.st_uid, metadata.st_gid), (os.geteuid(), os.getegid()))
        for name, content in KEYS.items():
            for directory in (fixture.key_dir, fixture.runtime_dir):
                path = directory / name
                self.assertEqual(path.read_bytes(), content)
                metadata = path.stat()
                self.assertEqual(stat.S_IMODE(metadata.st_mode), 0o600)
                self.assertEqual((metadata.st_uid, metadata.st_gid), (os.geteuid(), os.getegid()))
            self.assertFalse((fixture.runtime_dir / f"{name}.next").exists())

    def test_start_preserves_bytes_and_protects_files_before_docker(self) -> None:
        fixture = self.fixture()
        result = self.start(fixture)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, b"")
        self.assertEqual(result.stderr, b"")
        self.assertEqual(fixture.docker_calls.read_text().splitlines(), COMPOSE_ARGS)
        self.assert_runtime_keys(fixture)

    def test_reload_atomically_replaces_files_without_rotating_keys(self) -> None:
        fixture = self.fixture()
        fixture.runtime_dir.mkdir()
        fixture.runtime_dir.chmod(0o755)
        for name in KEYS:
            path = fixture.runtime_dir / name
            _ = path.write_bytes(b"previous runtime fixture")
            path.chmod(0o644)
            staged = fixture.runtime_dir / f"{name}.next"
            _ = staged.write_bytes(b"interrupted copy fixture")
            staged.chmod(0o666)

        with ExitStack() as files:
            old_files = {
                name: files.enter_context((fixture.runtime_dir / name).open("rb"))
                for name in KEYS
            }
            result = self.start(fixture)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(fixture.docker_calls.read_text().splitlines(), COMPOSE_ARGS)
            self.assert_runtime_keys(fixture)
            for name, previous in old_files.items():
                self.assertEqual(previous.read(), b"previous runtime fixture")
                self.assertNotEqual(
                    os.fstat(previous.fileno()).st_ino,
                    (fixture.runtime_dir / name).stat().st_ino,
                )

    def test_missing_or_empty_key_aborts_before_docker_without_runtime_fallback(self) -> None:
        for existing_runtime in (False, True):
            for name in KEYS:
                for missing in (True, False):
                    with self.subTest(runtime=existing_runtime, key=name, missing=missing):
                        fixture = self.fixture()
                        if existing_runtime:
                            fixture.runtime_dir.mkdir(mode=0o700)
                            for runtime_name in KEYS:
                                _ = (fixture.runtime_dir / runtime_name).write_bytes(b"existing runtime fixture")
                        source = fixture.key_dir / name
                        if missing:
                            source.unlink()
                        else:
                            _ = source.write_bytes(b"")
                        result = self.start(fixture)
                        self.assertNotEqual(result.returncode, 0)
                        self.assertFalse(fixture.docker_calls.exists())
                        self.assertEqual(result.stdout, b"")
                        self.assertTrue(result.stderr)
                        if existing_runtime:
                            for runtime_name in KEYS:
                                self.assertEqual(
                                    (fixture.runtime_dir / runtime_name).read_bytes(),
                                    b"existing runtime fixture",
                                )
                                self.assertFalse((fixture.runtime_dir / f"{runtime_name}.next").exists())
                        else:
                            self.assertFalse(fixture.runtime_dir.exists())

    def test_docker_failure_is_propagated(self) -> None:
        fixture = self.fixture()
        fixture.env["DOCKER_STATUS"] = "23"
        result = self.start(fixture)
        self.assertEqual(result.returncode, 23)
        self.assertEqual(fixture.docker_calls.read_text().splitlines(), COMPOSE_ARGS)
        self.assert_runtime_keys(fixture)


if __name__ == "__main__":
    _ = unittest.main()
