import json
import subprocess
import unittest
from pathlib import Path


class DeploymentTests(unittest.TestCase):
    def test_compose_runs_the_account_aware_extension(self) -> None:
        compose = Path(__file__).resolve().parents[1] / "docker-compose.yml"
        result = subprocess.run(
            ["docker", "compose", "-f", str(compose), "config", "--format", "json"],
            check=True, capture_output=True,
        )
        command = json.loads(result.stdout)["services"]["gemini-web2api"]["command"]
        self.assertEqual(command[:3], ["python", "-m", "extension"],
                         "Compose must not override the image with the legacy single-account module")
