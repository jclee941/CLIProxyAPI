from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("cliproxy-compose")


class CoreStartupTests(unittest.TestCase):
    def test_core_launcher_needs_no_sidecar_files_or_secret_cli(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docker = root / "docker"
            docker.write_text('#!/bin/sh\nprintf "%s\\n" "$@"\nexit "${DOCKER_STATUS:-0}"\n')
            docker.chmod(0o755)
            for forbidden in ("op", "install"):
                command = root / forbidden
                command.write_text("#!/bin/sh\nexit 98\n")
                command.chmod(0o755)
            for status in (0, 23):
                with self.subTest(status=status):
                    result = subprocess.run(
                        ["/bin/sh", str(SCRIPT), "config", "--services"],
                        env={"PATH": str(root), "DOCKER_STATUS": str(status)},
                        capture_output=True, text=True, check=False,
                    )
                    self.assertEqual(result.returncode, status, result.stderr)
                    self.assertEqual(result.stdout.splitlines(), [
                        "compose", "-f", "/opt/dashboard/docker-compose.dashboard-only.yml",
                        "--project-directory", "/opt/dashboard", "config", "--services",
                    ])
                    self.assertNotIn("chatgpt", result.stdout)
                    self.assertNotIn("web2api", result.stdout)


if __name__ == "__main__":
    unittest.main()
