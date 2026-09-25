# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
import json
from pathlib import Path
import subprocess
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .stage_parsing import decode, is_mapping, is_sequence, is_text, narrow
else:
    from stage_parsing import decode, is_mapping, is_sequence, is_text, narrow


def maintain() -> None:
    containers = decode(subprocess.check_output(["docker", "inspect", "cliproxyapi"]), is_sequence)
    config = narrow(narrow(containers[0], is_mapping)["Config"], is_mapping)
    environment = dict(narrow(value, is_text).split("=", 1) for value in narrow(config["Env"], is_sequence))
    accounts = decode(Path("/opt/dashboard/gemini-web-local/accounts.json").read_text(), is_sequence)
    for value in accounts:
        account_id = narrow(value, is_text)
        stored = decode((Path("/opt/dashboard/auths") / account_id).read_text(), is_mapping)
        assert narrow(stored["token_ref"], is_text).startswith("session://gemini-web/")
        curl = "\n".join([
            "silent", 'proxy = ""', 'url = "http://127.0.0.1:18318/v0/management/plugins/gemini-web/maintain"',
            'request = "POST"', 'header = "Content-Type: application/json"',
            "header = " + json.dumps("Authorization: Bearer " + environment["MANAGEMENT_PASSWORD"]),
            "data = " + json.dumps(json.dumps({"id": account_id})), 'write-out = "\\n%{http_code}"',
        ])
        response = subprocess.run(["curl", "--config", "-"], input=curl, text=True, capture_output=True)
        if response.returncode:
            raise SystemExit("maintenance_transport_failed; inspect before retry")
        raw, status = response.stdout.rsplit("\n", 1)
        assert status == "200"
        body = decode(raw, is_mapping)
        results = narrow(body["results"], is_sequence)
        assert len(results) == 1
        result = narrow(results[0], is_mapping)
        state = narrow(result["state"], is_text)
        print(json.dumps({"id": account_id, "state": state}))
        if state not in ("ready", "cooldown", "disabled_skip", "busy_skip"):
            raise SystemExit("maintenance_requires_operator")


if __name__ == "__main__":
    try:
        maintain()
    except (ValueError, AssertionError, SystemExit, subprocess.CalledProcessError):
        _ = subprocess.run(["systemctl", "stop", "gemini-web-local-maintenance.timer"], check=True)
        raise
