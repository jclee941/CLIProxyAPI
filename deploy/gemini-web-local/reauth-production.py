# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Run on .114: python3 reauth-production.py [--dry-run].

Repairs only an account already reporting a definitive auth_error. It never
touches a healthy account, sends at most one login completion per account per
run, and stops on any non-definitive outcome instead of retrying.
"""

import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
from typing import Final
import urllib.error
import urllib.request

BASE: Final = "http://127.0.0.1:18318"
LOGIN: Final = "/v0/management/plugins/gemini-web/login/"
ACCOUNTS: Final = "/v0/management/plugins/gemini-web/accounts"
ORIGIN: Final = "https://cliproxy.jclee.me"
EXTENSION: Final = "jkomnpgdokmnojgbapacnnkakgnbekjn"
PROFILES: Final = Path("/var/lib/gemini-login-desktop/home/chrome-migrated")
APPLIES: Final = (".google.com", "google.com", ".gemini.google.com", "gemini.google.com")

BINDINGS: Final = {
    "gemini-web-1c2f50176f190c7cecc246cb6cc86e33.json": ("Profile 5", "sunmin938a@gmail.com", "ec9f83f32c70467e4f7e2736dd0740c4cca56c3cd53a0854001771dd90c478c6"),
    "gemini-web-76a618ccab54cf1923743e283accb389.json": ("Profile 2", "qws941701@gmail.com", "3c6c833e8e0e5fd91caf22b2ba1258001049a43621cf8068a61b1eac1da6dc49"),
    "gemini-web-83fa747683343148070a7386bbe73e11.json": ("Default", "qws941704@gmail.com", "2f9a80e88f42ade458086cd55a7d09fa2bd75672b837b3d9365593a5bf85bede"),
    "gemini-web-a7a8abe7b64ba5d2256eb799e9902882.json": ("Profile 4", "qws941702@gmail.com", "431322b8a4f251addb60932bf35c5a3499aeaff4683c086918b4b9cfc23b2ead"),
    "gemini-web-profile-1.json": ("Profile 1", "qwer941a@gmail.com", "15cb774936d2ffba32bebc4b80a84b33964df3b408f79c663c6cd63e0b9ca400"),
}
AUTH_USER: Final = 0


class Stop(Exception):
    """An outcome that must be investigated instead of retried."""


def management_key() -> str:
    raw = subprocess.check_output(["docker", "inspect", "cliproxyapi", "--format", "{{range .Config.Env}}{{println .}}{{end}}"], text=True)
    for line in raw.splitlines():
        name, separator, value = line.partition("=")
        if separator and name == "MANAGEMENT_PASSWORD" and value:
            return value
    raise Stop("management_key_unavailable")


def call(key: str, path: str, payload: object | None, origin: bool) -> tuple[int, object]:
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(BASE + path, data=data, method="POST" if data else "GET")
    request.add_header("Authorization", "Bearer " + key)
    if data:
        request.add_header("Content-Type", "application/json")
    if origin:
        request.add_header("Origin", ORIGIN)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request) as response:
            return response.status, json.loads(response.read().decode() or "{}")
    except urllib.error.HTTPError as error:
        return error.code, json.loads(error.read().decode() or "{}")
    except OSError as error:
        raise Stop("transport_outcome_unknown") from error


def cookie_header(profile: str) -> str:
    source = PROFILES / profile / "Cookies"
    if not source.is_file():
        raise Stop("cookie_database_missing")
    scratch = Path(os.environ.get("RUNTIME_DIRECTORY") or "/dev/shm") / ("gemini-reauth-" + hashlib.sha256(profile.encode()).hexdigest()[:12])
    scratch.write_bytes(source.read_bytes())
    try:
        aes = hashlib.pbkdf2_hmac("sha1", b"peanuts", b"saltysalt", 1, 16)
        now = int((datetime.datetime.now(datetime.UTC).replace(tzinfo=None) - datetime.datetime(1601, 1, 1)).total_seconds() * 1_000_000)
        pairs: list[tuple[str, str]] = []
        with sqlite3.connect(scratch) as database:
            rows = database.execute("select host_key,name,encrypted_value,path,expires_utc,is_persistent from cookies order by name").fetchall()
        for host, name, encrypted, path, expires, persistent in rows:
            if host not in APPLIES or path != "/":
                continue
            if persistent and expires and expires <= now:
                continue
            value = decrypt(bytes(encrypted), host, aes)
            if value is None:
                raise Stop("cookie_decode_failed")
            pairs.append((name, value))
        if not pairs:
            raise Stop("no_applicable_cookies")
        return "; ".join(name + "=" + value for name, value in pairs)
    finally:
        scratch.unlink(missing_ok=True)


def decrypt(blob: bytes, host: str, aes: bytes) -> str | None:
    body = blob[3:]
    if blob[:3] != b"v10" or not body or len(body) % 16:
        return None
    result = subprocess.run(
        ["openssl", "enc", "-d", "-aes-128-cbc", "-K", aes.hex(), "-iv", (b" " * 16).hex(), "-nopad"],
        input=body, capture_output=True)
    if result.returncode or not result.stdout:
        return None
    plain = result.stdout
    padding = plain[-1]
    if 1 <= padding <= 16 and plain[-padding:] == bytes([padding]) * padding:
        plain = plain[:-padding]
    if plain[:32] == hashlib.sha256(host.encode()).digest():
        plain = plain[32:]
    try:
        text = plain.decode("ascii")
    except UnicodeDecodeError:
        return None
    return text if all(32 <= ord(character) <= 126 for character in text) else None


def reauthenticate(key: str, account: str, dry_run: bool) -> str:
    profile, label, digest = BINDINGS[account]
    token = "gemini-web:v1:" + base64.urlsafe_b64encode(
        json.dumps({"cookie": cookie_header(profile), "auth_user": AUTH_USER}, separators=(",", ":")).encode()).decode().rstrip("=")
    if dry_run:
        return "captured_only"
    status, flow = call(key, LOGIN + "start", {"label": label, "existing_id": account, "consent": True}, True)
    if status != 200 or not isinstance(flow, dict) or flow.get("status") != "pending":
        raise Stop("login_start_rejected")
    identity = flow.get("expected_identity")
    if not isinstance(identity, dict) or identity.get("account_sha256") != digest or identity.get("auth_user") != AUTH_USER:
        raise Stop("identity_mismatch")
    state = flow.get("state")
    if not isinstance(state, str):
        raise Stop("login_state_invalid")
    status, view = call(key, LOGIN + "complete", {
        "state": state, "token": token, "account_sha256": digest,
        "auth_user": AUTH_USER, "extension_id": EXTENSION, "consent": True}, True)
    if status != 200 or not isinstance(view, dict):
        raise Stop("login_complete_unknown_outcome")
    result = view.get("status")
    if result not in ("ready", "saved"):
        raise Stop("login_complete_not_ready")
    return str(result)


def main() -> None:
    _ = os.umask(0o077)
    dry_run = "--dry-run" in sys.argv[1:]
    key = management_key()
    status, listing = call(key, ACCOUNTS, None, False)
    if status != 200 or not isinstance(listing, dict):
        raise Stop("accounts_unavailable")
    accounts = listing.get("accounts")
    if not isinstance(accounts, list):
        raise Stop("accounts_invalid")
    repaired = 0
    for entry in accounts:
        if not isinstance(entry, dict):
            raise Stop("account_entry_invalid")
        account = entry.get("id")
        if account not in BINDINGS or not entry.get("enabled"):
            continue
        if entry.get("status") != "expired" or entry.get("error") != "auth_error":
            continue
        outcome = reauthenticate(key, str(account), dry_run)
        repaired += 1
        print(json.dumps({"account": account, "outcome": outcome}))
    print(json.dumps({"checked": len(accounts), "reauthenticated": repaired, "dry_run": dry_run}))


if __name__ == "__main__":
    try:
        main()
    except Stop as stop:
        raise SystemExit(str(stop))
