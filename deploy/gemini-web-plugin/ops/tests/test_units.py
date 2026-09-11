import configparser
from pathlib import Path
import subprocess
import tempfile
import unittest
from typing import Final


OPS: Final = Path(__file__).resolve().parents[1]
SERVICE: Final = "gemini-session-maintenance.service"
TIMER: Final = "gemini-session-maintenance.timer"


class UnitTests(unittest.TestCase):
    def test_completion_based_schedule_when_units_loaded(self) -> None:
        service = configparser.ConfigParser(strict=False, interpolation=None)
        timer = configparser.ConfigParser(interpolation=None)
        _ = service.read(OPS / SERVICE)
        _ = timer.read(OPS / TIMER)
        self.assertEqual(service["Service"]["Type"], "oneshot")
        self.assertEqual(service["Service"]["Restart"], "no")
        self.assertEqual(service["Service"]["TimeoutStartSec"], "0")
        self.assertNotIn("RemainAfterExit", service["Service"])
        self.assertEqual(
            service["Service"]["ExecStart"],
            "/usr/bin/op run -- /usr/bin/python3 /opt/gemini-web-plugin/ops/maintain.py",
        )
        self.assertEqual(
            service["Service"]["EnvironmentFile"],
            "/etc/cliproxy/op-service-account.env",
        )
        self.assertEqual(timer["Timer"]["Unit"], SERVICE)
        self.assertEqual(timer["Timer"]["OnBootSec"], "30s")
        self.assertEqual(timer["Timer"]["OnUnitInactiveSec"], "5min")
        self.assertNotIn("Persistent", timer["Timer"])
        self.assertNotIn("OnUnitActiveSec", timer["Timer"])

    def test_systemd_accepts_units_when_isolated_fixture_root(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            units = root / "etc/systemd/system"
            units.mkdir(parents=True)
            for name in (SERVICE, TIMER):
                _ = (units / name).write_bytes((OPS / name).read_bytes())
            for name in (
                "sysinit.target",
                "basic.target",
                "shutdown.target",
                "timers.target",
                "network-online.target",
            ):
                _ = (units / name).write_text(
                    "[Unit]\nDescription=Local validation fixture\n"
                )
            binaries = root / "usr/bin"
            binaries.mkdir(parents=True)
            for name in ("op", "python3"):
                fixture = binaries / name
                _ = fixture.write_text("#!/bin/sh\nexit 0\n")
                fixture.chmod(0o755)
            result = subprocess.run(
                ["systemd-analyze", "--root", directory, "verify", SERVICE, TIMER],
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


if __name__ == "__main__":
    _ = unittest.main()
