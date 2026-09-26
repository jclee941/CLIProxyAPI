import configparser
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from typing import Final


OPS: Final = Path(__file__).resolve().parents[1]
SERVICE: Final = "gemini-session-maintenance.service"


class UnitTests(unittest.TestCase):
    def test_oneshot_service_when_unit_loaded(self) -> None:
        service = configparser.ConfigParser(strict=False, interpolation=None)
        _ = service.read(OPS / SERVICE)
        self.assertEqual(service["Service"]["Type"], "oneshot")
        self.assertEqual(service["Service"]["Restart"], "no")
        self.assertEqual(service["Service"]["TimeoutStartSec"], "0")
        self.assertNotIn("RemainAfterExit", service["Service"])
        self.assertEqual(
            service["Service"]["ExecStart"],
            "/bin/sh /opt/gemini-web-plugin/ops/run.sh",
        )
        self.assertNotIn("EnvironmentFile", service["Service"])

    def test_systemd_accepts_units_when_isolated_fixture_root(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            units = root / "etc/systemd/system"
            units.mkdir(parents=True)
            _ = (units / SERVICE).write_bytes((OPS / SERVICE).read_bytes())
            for name in (
                "sysinit.target",
                "basic.target",
                "shutdown.target",
                "network-online.target",
            ):
                _ = (units / name).write_text(
                    "[Unit]\nDescription=Local validation fixture\n"
                )
            binaries = root / "usr/bin"
            binaries.mkdir(parents=True)
            for name in ("python3",):
                fixture = binaries / name
                _ = fixture.write_text("#!/bin/sh\nexit 0\n")
                fixture.chmod(0o755)
            shell = root / "bin/sh"
            shell.parent.mkdir(parents=True)
            shell.write_text("#!/bin/sh\nexit 0\n")
            shell.chmod(0o755)
            result = subprocess.run(
                ["systemd-analyze", "--root", directory, "verify", SERVICE],
                capture_output=True,
                text=True,
                check=False,
                timeout=20,
            )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stderr, "")

    def test_shell_syntax_when_manual_launcher_parsed(self) -> None:
        result = subprocess.run(
            ["/bin/sh", "-n", str(OPS / "run.sh")],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_launcher_uses_local_credentials_without_op(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            credentials = root / "core.env"
            credentials.write_text("MANAGEMENT_PASSWORD='synthetic-local-key'\n")
            credentials.chmod(0o600)
            python = root / "python3"
            python.write_text(
                '#!/usr/bin/python3\n'
                'import runpy, sys\n'
                'runner = runpy.run_path(sys.argv[1])\n'
                'assert runner["management_key"]() == "synthetic-local-key"\n'
                'assert sys.argv[2:] == ["--id", "gemini-web-fixture.json"]\n'
                'print("LOCAL_KEY_OK")\n'
            )
            python.chmod(0o755)
            op = root / "op"
            op.write_text('#!/bin/sh\nprintf "OP_MUST_NOT_RUN\\n" >&2\nexit 99\n')
            op.chmod(0o755)
            result = subprocess.run(
                ["/bin/sh", str(OPS / "run.sh"), "--id", "gemini-web-fixture.json"],
                env={**os.environ, "PATH": f"{root}:/usr/bin:/bin", "GEMINI_MAINTENANCE_ENV_FILE": str(credentials)},
                capture_output=True, text=True, check=False,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "LOCAL_KEY_OK\n")
        self.assertNotIn("synthetic-local-key", result.stdout + result.stderr)

    def test_launcher_missing_local_key_fails_before_http(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            credentials = root / "core.env"
            credentials.write_text("MANAGEMENT_PASSWORD=''\n")
            for path in (credentials, root / "missing.env"):
                with self.subTest(path=path.name):
                    result = subprocess.run(
                        ["/bin/sh", str(OPS / "run.sh")],
                        env={**os.environ, "PATH": f"{root}:/usr/bin:/bin", "GEMINI_MAINTENANCE_ENV_FILE": str(path)},
                        capture_output=True, text=True, check=False,
                    )
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("maintenance error=invalid_key", result.stderr)


if __name__ == "__main__":
    _ = unittest.main()
