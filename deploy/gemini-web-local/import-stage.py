# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Private .114 intake commands; credentials travel through curl's stdin only."""

from dataclasses import dataclass
import fcntl
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import TYPE_CHECKING, Final

if TYPE_CHECKING:
    from .stage_parsing import Receipt, decode, is_account_list, is_auth_record, is_bundle, is_config, is_flow, is_native_list, is_originals, is_receipt
else:
    from stage_parsing import Receipt, decode, is_account_list, is_auth_record, is_bundle, is_config, is_flow, is_native_list, is_originals, is_receipt

ROOT: Final = Path("/root/.local/state/gemini-web-importer")
BOOTSTRAP: Final = Path("/etc/cliproxy/gemini-web-importer")
ORIGIN: Final = "https://cliproxy.jclee.me"
EXTENSION: Final = "jkomnpgdokmnojgbapacnnkakgnbekjn"
PREFIX: Final = "/v0/management/plugins/gemini-web/login/"


@dataclass(frozen=True, slots=True)
class Reply:
    status: int
    body: str


def request(method: str, path: str, payload: str = "") -> Reply:
    key = (BOOTSTRAP / ("api.key" if path == "/v1/models" else "management.key")).read_text().strip()
    config = "\n".join([
        "silent", "show-error", 'proxy = ""',
        "url = " + json.dumps("http://127.0.0.1:18319" + path),
        "request = " + json.dumps(method),
        "header = " + json.dumps("Authorization: Bearer " + key),
        "header = " + json.dumps("Origin: " + ORIGIN),
        'header = "Content-Type: application/json"',
        'write-out = "\\n%{http_code}"',
    ])
    if payload:
        config += "\ndata = " + json.dumps(payload)
    result = subprocess.run(["curl", "--config", "-"], input=config, text=True, capture_output=True)
    if result.returncode:
        raise SystemExit("transport_failure; unknown outcome; no automatic retry")
    body, status = result.stdout.rsplit("\n", 1)
    return Reply(int(status), body)


def operation(name: str, body: str) -> str:
    reply = request("POST", PREFIX + name, body)
    if reply.status != 200:
        raise SystemExit(f"login_{name}_http_{reply.status}; inspect privately; no retry")
    return reply.body


def probe() -> None:
    native = request("GET", "/v0/management/auth-files")
    assert native.status == 200
    native_list = decode(native.body, is_native_list)
    records = native_list["files"]
    assert len(records) == 5 and all(record["disabled"] for record in records)
    listed = request("GET", "/v0/management/plugins/gemini-web/accounts")
    assert listed.status == 200
    account_list = decode(listed.body, is_account_list)
    accounts = account_list["accounts"]
    assert len(accounts) == 5 and all(not account["enabled"] and not account["models"] for account in accounts)
    models = request("GET", "/v1/models")
    assert models.status == 200 and '"gemini-web-' not in models.body
    originals = decode((ROOT / "originals/mappings.json").read_text(), is_originals)
    flow = decode(operation("start", json.dumps({"label": "staging-readiness-probe", "consent": True})), is_flow)
    assert "extension_id" in flow and "manager_origin" in flow
    assert flow["status"] == "pending" and flow["extension_id"] == EXTENSION and flow["manager_origin"] == ORIGIN
    cancelled = decode(operation("cancel", json.dumps({"state": flow["state"]})), is_flow)
    assert cancelled["status"] == "cancelled"
    config = decode((ROOT / "config.yaml").read_text(), is_config)
    for account_id, original in originals.items():
        binding = config["plugins"]["configs"]["gemini-web"]["maintenance_sources"][account_id]
        if "auth_user" not in binding:
            blocked = request("POST", PREFIX + "start", json.dumps({"label": "staging-readiness-probe", "existing_id": account_id, "consent": True}))
            assert blocked.status == 409
            continue
        flow = decode(operation("start", json.dumps({"label": "staging-readiness-probe", "existing_id": account_id, "consent": True})), is_flow)
        assert "extension_id" in flow and "manager_origin" in flow and "expected_identity" in flow
        assert flow["status"] == "pending" and flow["extension_id"] == EXTENSION and flow["manager_origin"] == ORIGIN
        identity = flow["expected_identity"]
        assert identity["account_sha256"] == original["binding"]["expected_gaia_sha256"]
        assert identity["auth_user"] == binding["auth_user"]
        cancelled = decode(operation("cancel", json.dumps({"state": flow["state"]})), is_flow)
        assert cancelled["status"] == "cancelled"
    print("ready: management_authenticated; five_disabled_accounts; exact_origin_extension_start_cancel; missing_legacy_indices_fail_closed; zero_linked_claims")


def bind_indices(path: Path) -> None:
    resolved = path.resolve(strict=True)
    assert resolved.is_relative_to(ROOT / "intake") and resolved.stat().st_uid == 0 and resolved.stat().st_mode & 0o077 == 0
    bundle = decode(resolved.read_text(), is_bundle)
    assert bundle["version"] == 1 and bundle["consent"] is True
    assert re.fullmatch(r"[0-9a-f]{32}", bundle["bundle_id"])
    originals = decode((ROOT / "originals/mappings.json").read_text(), is_originals)
    config_path = ROOT / "config.yaml"
    config = decode(config_path.read_text(), is_config)
    bindings = config["plugins"]["configs"]["gemini-web"]["maintenance_sources"]
    for capture in bundle["accounts"]:
        assert capture["validated"] is True and capture["consent"] is True
        assert type(capture["auth_user"]) is int and capture["auth_user"] >= 0
        account_id = capture["existing_id"]
        if not account_id:
            assert all(original["binding"]["expected_gaia_sha256"] != capture["account_sha256"] for original in originals.values())
            continue
        assert originals[account_id]["binding"]["expected_gaia_sha256"] == capture["account_sha256"]
        if "auth_user" in bindings[account_id]:
            assert bindings[account_id].get("auth_user") == capture["auth_user"]
        bindings[account_id]["auth_user"] = capture["auth_user"]
    for receipt_path in (ROOT / "receipts").glob("*.json"):
        receipt = decode(receipt_path.read_text(), is_receipt)
        assert receipt["phase"] in ("saved", "ready")
    _ = subprocess.run(["docker", "stop", "gemini-web-importer"], check=True, stdout=subprocess.DEVNULL)
    _ = config_path.write_text(json.dumps(config))
    _ = subprocess.run(["docker", "start", "gemini-web-importer"], check=True, stdout=subprocess.DEVNULL)
    print("stage_indices_bound_from_verified_bundle; production_unchanged; verify_API_ready_before_import")


def intake(path: Path) -> None:
    resolved = path.resolve(strict=True)
    if not resolved.is_relative_to(ROOT / "intake") or resolved.stat().st_mode & 0o077 or resolved.stat().st_uid != 0:
        raise SystemExit("intake_requires_root_owned_0600_file_inside_private_intake")
    bundle = decode(resolved.read_text(), is_bundle)
    assert bundle["version"] == 1 and bundle["consent"] is True
    assert re.fullmatch(r"[0-9a-f]{32}", bundle["bundle_id"])
    originals = decode((ROOT / "originals/mappings.json").read_text(), is_originals)
    for index, capture in enumerate(bundle["accounts"]):
        assert capture["validated"] is True and capture["consent"] is True
        assert type(capture["auth_user"]) is int and capture["auth_user"] >= 0
        assert re.fullmatch(r"[0-9a-f]{64}", capture["account_sha256"])
        assert 0 < len(capture["label"]) <= 200 and not any(char in capture["label"] for char in "\r\n\0")
        assert capture["token"].startswith("gemini-web:v1:") and len(capture["token"]) <= 32768
        account_id = capture["existing_id"]
        if account_id:
            original = originals[account_id]
            assert capture["account_sha256"] == original["binding"]["expected_gaia_sha256"]
            config = decode((ROOT / "config.yaml").read_text(), is_config)
            assert capture["auth_user"] == config["plugins"]["configs"]["gemini-web"]["maintenance_sources"][account_id].get("auth_user")
        else:
            assert all(original["binding"]["expected_gaia_sha256"] != capture["account_sha256"] for original in originals.values())
        receipt_path = ROOT / "receipts" / (account_id or f"new-{bundle['bundle_id']}-{index}.json")
        with receipt_path.open("x") as receipt_file:
            _ = receipt_file.write(json.dumps({"account_id": account_id, "phase": "starting"}))
        flow = decode(operation("start", json.dumps({"label": capture["label"], "existing_id": account_id, "consent": True})), is_flow)
        receipt: Receipt = {"account_id": account_id, "state": flow["state"], "phase": "completing"}
        _ = receipt_path.write_text(json.dumps(receipt))
        completion = decode(operation("complete", json.dumps({
            "state": flow["state"], "token": capture["token"], "account_sha256": capture["account_sha256"],
            "auth_user": capture["auth_user"], "extension_id": EXTENSION, "consent": True,
        })), is_flow)
        receipt["phase"] = completion["status"]
        if completion["status"] in ("saved", "ready") and not account_id:
            assert "account_id" in completion
            receipt["account_id"] = completion["account_id"]
        _ = receipt_path.write_text(json.dumps(receipt))
        if completion["status"] not in ("saved", "ready"):
            raise SystemExit("import_requires_operator; receipt retained; no retry")
        stored = decode((ROOT / "auths" / receipt["account_id"]).read_text(), is_auth_record)
        assert stored["id"] == receipt["account_id"]
        assert stored.get("disabled", False) == bool(account_id)
        assert stored["token_ref"].startswith("session://gemini-web/") and stored["label"] == capture["label"]
        print(json.dumps({"id": receipt["account_id"], "status": completion["status"], "disabled": stored.get("disabled", False), "label_match": True, "receipt": receipt_path.name}))


def main() -> None:
    match sys.argv[1:]:
        case ["probe"]:
            probe()
        case ["import", filename]:
            intake(Path(filename))
        case ["bind-indices", filename]:
            bind_indices(Path(filename))
        case ["status", filename] | ["reconcile", filename]:
            receipt_path = Path(filename).resolve(strict=True)
            assert receipt_path.is_relative_to(ROOT / "receipts")
            receipt = decode(receipt_path.read_text(), is_receipt)
            assert "state" in receipt
            result = decode(operation(sys.argv[1], json.dumps({"state": receipt["state"]})), is_flow)
            print(json.dumps({"id": receipt["account_id"], "status": result["status"], "models_ready": result["models_ready"]}))
        case _:
            raise SystemExit("usage: import-stage.py probe | bind-indices PRIVATE_BUNDLE | import PRIVATE_BUNDLE | status PRIVATE_RECEIPT | reconcile PRIVATE_RECEIPT")


if __name__ == "__main__":
    _ = os.umask(0o077)
    with (ROOT / "operator.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        main()
