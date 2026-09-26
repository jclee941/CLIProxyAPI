#!/usr/bin/env python3
"""Converge the CPA core and native plugins on this host to what the deploy clone declares.

pull-deploy.sh runs this from the deploy clone after moving the clone to the
fork's master branch. deploy/cpa-plugins.json names every artifact the host
serves, and anything whose bytes differ from the live copy is replaced:

- a dashboard file is replaced in place, since the host reads it per request;
- a "hot" plugin is swapped and re-enabled through the management API;
- a "restart" plugin holds the plugin session store, so it is swapped under a
  stopped container, and only while no Omni turn runs: a restart kills every
  generation in flight;
- the core executable is the core-build workflow's published build of the last
  master commit that touched the core paths, swapped with the restart plugins.

Every tick compares again, so a tick that finds the host busy leaves the swap
to the next one. A swap the host does not come back from is followed by one
container restart and never by a rollback: what runs is decided by moving master.
Compose is never used here; the core image cannot be pulled on this host, and
a compose run that fails to pull takes the container off its network.
"""

from __future__ import annotations

import dataclasses
import datetime
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from typing import Any

LOG_LINE = re.compile(r"^\[(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)\] \[([0-9a-f]{8})\]")


class ConvergeError(Exception):
    pass


@dataclasses.dataclass(frozen=True)
class Settings:
    clone: pathlib.Path
    manifest: pathlib.Path
    plugin_dir: pathlib.Path
    core_binary: pathlib.Path
    container: str
    container_plugin_dir: str
    api: str
    core_env: pathlib.Path
    idle_confirm: float
    health_wait: float
    poll: float
    dry_run: bool

    @classmethod
    def from_env(cls, manifest: str) -> "Settings":
        clone = pathlib.Path.cwd()
        env = os.environ
        return cls(
            clone=clone,
            manifest=clone / manifest,
            plugin_dir=pathlib.Path(env.get("CPA_PLUGIN_DIR", "/opt/dashboard/plugins")),
            core_binary=pathlib.Path(env.get("CPA_CORE_BINARY", "/opt/dashboard/gemini-web/CLIProxyAPI")),
            container=env.get("CPA_CONTAINER", "cliproxyapi"),
            container_plugin_dir=env.get("CPA_CONTAINER_PLUGIN_DIR", "/CLIProxyAPI/plugins"),
            api=env.get("CPA_API", "http://127.0.0.1:18318").rstrip("/"),
            core_env=pathlib.Path(env.get("CPA_CORE_ENV", "/etc/cliproxy/gemini-web/core.env")),
            idle_confirm=float(env.get("CPA_IDLE_CONFIRM_SECONDS", "65")),
            health_wait=float(env.get("CPA_HEALTH_WAIT_SECONDS", "180")),
            poll=float(env.get("CPA_POLL_SECONDS", "3")),
            dry_run=env.get("CPA_DRY_RUN") == "1",
        )


@dataclasses.dataclass
class PluginChange:
    id: str
    reload: str
    artifact: pathlib.Path
    live: list[pathlib.Path]


@dataclasses.dataclass
class CoreChange:
    commit: str
    url: str
    digest: str


def log(message: str) -> None:
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    print(f"{stamp} cpa-converge: {message}", flush=True)


def digest(path: pathlib.Path) -> str:
    sha = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            sha.update(block)
    return sha.hexdigest()


def loadable(plugin_dir: pathlib.Path, plugin_id: str) -> list[pathlib.Path]:
    """The files the host would load for an id: <id>.so or <id>-v<version>.so."""
    found = []
    for path in plugin_dir.iterdir():
        name = path.name
        if not path.is_file() or not name.endswith(".so"):
            continue
        if name == plugin_id + ".so" or re.fullmatch(re.escape(plugin_id) + r"-v[0-9][0-9A-Za-z.+-]*\.so", name):
            found.append(path)
    return sorted(found)


def run(*args: str) -> str:
    result = subprocess.run(list(args), capture_output=True, text=True)
    if result.returncode != 0:
        raise ConvergeError(f"{' '.join(args[:3])} exited {result.returncode}: {result.stderr.strip()[:300]}")
    return result.stdout


def management_key(settings: Settings) -> str:
    for line in settings.core_env.read_text().splitlines():
        if line.startswith("MANAGEMENT_PASSWORD="):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    raise ConvergeError(f"MANAGEMENT_PASSWORD is missing from {settings.core_env}")


def call(settings: Settings, key: str, method: str, path: str, body: object = None) -> Any:
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(
        settings.api + path,
        data=data,
        method=method,
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=120) as response:
        return json.loads(response.read() or b"null")


def active_plugins(settings: Settings, key: str) -> dict[str, tuple[str, bool]]:
    listing = call(settings, key, "GET", "/v0/management/plugins")
    return {entry["id"]: (entry.get("path", ""), bool(entry.get("registered"))) for entry in listing.get("plugins", [])}


def accounts(settings: Settings, key: str) -> list[dict[str, Any]]:
    return call(settings, key, "GET", "/v0/management/plugins/gemini-web/accounts").get("accounts", [])


def generating(settings: Settings, key: str) -> int:
    return sum(1 for account in accounts(settings, key) if account.get("status") == "generating" or account.get("activity"))


def inflight(settings: Settings) -> int:
    """Omni requests picked in the last twelve minutes that have not answered.

    The container logs in its own time zone, so the cutoff is taken from its
    clock rather than this host's.
    """
    now = datetime.datetime.strptime(run("docker", "exec", settings.container, "date", "+%Y-%m-%d %H:%M:%S").strip(), "%Y-%m-%d %H:%M:%S")
    cutoff = (now - datetime.timedelta(minutes=12)).strftime("%Y-%m-%d %H:%M:%S")
    tail = run("docker", "exec", settings.container, "sh", "-c", "tail -n 40000 /CLIProxyAPI/logs/main.log")
    picked, answered = set(), set()
    for line in tail.splitlines():
        match = LOG_LINE.match(line)
        if not match or match.group(1) < cutoff:
            continue
        if "provider=gemini-web auth_file=" in line and "for model gemini-omni" in line:
            picked.add(match.group(2))
        elif "gin_logger.go" in line and "/v1beta/interactions" in line:
            answered.add(match.group(2))
    return len(picked - answered)


def idle(settings: Settings, key: str) -> bool:
    """No Omni turn running, confirmed across one listing cache refresh.

    A turn keeps generating inside the plugin after its HTTP request ends, so
    the log alone reads idle while an account still generates.
    """
    for attempt in range(2):
        if inflight(settings) or generating(settings, key):
            return False
        if attempt == 0:
            time.sleep(settings.idle_confirm)
    return True


def ready(settings: Settings, key: str) -> int:
    return sum(1 for account in accounts(settings, key) if account.get("status") == "ready")


def core_plan(settings: Settings, core: dict[str, Any]) -> CoreChange | None:
    """The published build of the last master commit that touched the core, unless it is live.

    Builds are not byte-reproducible, so the build's published sha256 decides.
    """
    commit = run("git", "-C", str(settings.clone), "log", "-1", "--first-parent", "--format=%H", "--", *core["paths"]).strip()[:7]
    if not commit:
        raise ConvergeError(f"no commit touches the core paths {core['paths']}")
    url = f"{core['release'].rstrip('/')}/{core['asset']}-{commit}"
    try:
        with urllib.request.urlopen(url + ".sha256", timeout=120) as response:
            fields = response.read().decode().split()
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        raise ConvergeError(f"core {commit} is not published yet; the core-build workflow publishes it") from error
    if not fields:
        raise ConvergeError(f"the published sha256 of core {commit} is empty")
    if settings.core_binary.exists() and digest(settings.core_binary) == fields[0]:
        return None
    return CoreChange(commit, url, fields[0])


def stage_core(change: CoreChange, staged: pathlib.Path) -> None:
    with urllib.request.urlopen(change.url, timeout=120) as response, staged.open("wb") as handle:
        shutil.copyfileobj(response, handle, 1 << 20)
    if digest(staged) != change.digest:
        raise ConvergeError(f"the core {change.commit} download does not match its published sha256")
    staged.chmod(0o755)


def install_core(settings: Settings, staged: pathlib.Path) -> None:
    """Replaces the host file the stopped container bind-mounts, so the next start runs it.

    The replaced executable stays as <name>.prev, which nothing runs.
    """
    target = settings.core_binary
    if target.exists():
        os.replace(target, target.with_name(target.name + ".prev"))
    os.replace(staged, target)


def core_commit(settings: Settings, key: str) -> str:
    request = urllib.request.Request(settings.api + "/v0/management/plugins", headers={"Authorization": "Bearer " + key})
    with urllib.request.urlopen(request, timeout=120) as response:
        return response.headers.get("X-CPA-COMMIT", "")


def plan(settings: Settings, manifest: dict[str, Any]) -> tuple[list[tuple[pathlib.Path, pathlib.Path]], list[PluginChange], CoreChange | None]:
    files = []
    for entry in manifest.get("files", []):
        artifact, target = settings.clone / entry["artifact"], settings.plugin_dir / entry["target"]
        if not target.exists() or digest(artifact) != digest(target):
            files.append((artifact, target))
    plugins = []
    for entry in manifest.get("plugins", []):
        if entry["reload"] not in ("hot", "restart"):
            raise ConvergeError(f"plugin {entry['id']} has unknown reload {entry['reload']!r}")
        artifact = settings.clone / entry["artifact"]
        live = loadable(settings.plugin_dir, entry["id"])
        if len(live) != 1 or digest(live[0]) != digest(artifact):
            plugins.append(PluginChange(entry["id"], entry["reload"], artifact, live))
    core = core_plan(settings, manifest["core"]) if "core" in manifest else None
    return files, plugins, core


def replace_file(artifact: pathlib.Path, target: pathlib.Path) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    staged = target.with_name(target.name + ".converging")
    shutil.copyfile(artifact, staged)
    staged.chmod(0o644)
    os.replace(staged, target)


def swap(settings: Settings, change: PluginChange, commit: str) -> pathlib.Path:
    """Installs the artifact as the only loadable file for its id.

    The version carries the commit so the host log names what it loaded; the
    replaced file keeps its bytes under a name the host does not load.
    """
    day = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d")
    target = settings.plugin_dir / f"{change.id}-v0.1.0.{day}.g{commit}.so"
    staged = target.with_name(target.name + ".converging")
    shutil.copyfile(change.artifact, staged)
    staged.chmod(0o644)
    for old in change.live:
        if old != target:
            old.rename(old.with_name(f"{old.name}.prev-g{commit}"))
    os.replace(staged, target)
    return target


def wait_until(settings: Settings, check) -> bool:
    deadline = time.monotonic() + settings.health_wait
    last_error = ""
    while True:
        try:
            if check():
                return True
        except (urllib.error.URLError, OSError, ValueError, KeyError) as error:
            last_error = str(error)
        if time.monotonic() >= deadline:
            if last_error:
                log(f"last health error: {last_error[:200]}")
            return False
        time.sleep(settings.poll)


def loaded(settings: Settings, key: str, expected: dict[str, pathlib.Path]) -> bool:
    active = active_plugins(settings, key)
    return all(active.get(plugin_id) == (f"{settings.container_plugin_dir}/{path.name}", True) for plugin_id, path in expected.items())


def restart_until(settings: Settings, key: str, check, reason: str) -> None:
    """One container restart for a host that did not come back, then give up."""
    if wait_until(settings, check):
        return
    log(f"{reason}; restarting the container once")
    run("docker", "restart", "-t", "240", settings.container)
    if not wait_until(settings, check):
        raise ConvergeError(f"{reason} after a container restart")


def converge(settings: Settings) -> int:
    manifest = json.loads(settings.manifest.read_text())
    commit = run("git", "-C", str(settings.clone), "rev-parse", "--short=7", "HEAD").strip()
    files, plugins, core = plan(settings, manifest)
    if not files and not plugins and core is None:
        log(f"in sync at {commit}")
        return 0
    for artifact, target in files:
        log(f"{'would replace' if settings.dry_run else 'replacing'} {target}")
    for change in plugins:
        log(f"{change.id} ({change.reload}) differs from the deploy clone; live: {[path.name for path in change.live] or 'none'}")
    if core is not None:
        log(f"core differs from the published build of {core.commit}")
    if settings.dry_run:
        return 0

    for artifact, target in files:
        replace_file(artifact, target)
    key = management_key(settings)

    for change in (change for change in plugins if change.reload == "hot"):
        installed = swap(settings, change, commit)
        call(settings, key, "PATCH", f"/v0/management/plugins/{change.id}/enabled", {"enabled": True})
        restart_until(settings, key, lambda: loaded(settings, key, {change.id: installed}), f"{change.id} did not load {installed.name}")
        log(f"{change.id} loaded {installed.name}")

    restarts = [change for change in plugins if change.reload == "restart"]
    if restarts or core is not None:
        staged = settings.core_binary.with_name(settings.core_binary.name + ".converging")
        expected_commit = core.commit if core is not None else ""
        try:
            if core is not None:
                stage_core(core, staged)
            if not idle(settings, key):
                log("an Omni turn is running; the restart waits for the next tick")
                return 0
            before = ready(settings, key)
            run("docker", "stop", "-t", "240", settings.container)
            swapped = {change.id: swap(settings, change, commit) for change in restarts}
            if core is not None:
                install_core(settings, staged)
            run("docker", "start", settings.container)
            everything = {entry["id"]: loadable(settings.plugin_dir, entry["id"])[0] for entry in manifest.get("plugins", [])}
            wanted = sorted(path.name for path in swapped.values()) + ([f"core {expected_commit}"] if expected_commit else [])
            restart_until(
                settings,
                key,
                lambda: loaded(settings, key, everything) and ready(settings, key) >= before and (not expected_commit or core_commit(settings, key) == expected_commit),
                f"the host did not come back with {wanted}, every plugin loaded and {before} ready accounts",
            )
        finally:
            staged.unlink(missing_ok=True)
        for plugin_id, path in swapped.items():
            log(f"{plugin_id} loaded {path.name}")
        if expected_commit:
            log(f"core runs {expected_commit}")
    log(f"converged at {commit}")
    return 0


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: cpa-converge.py <manifest relative to the deploy clone>", file=sys.stderr)
        return 2
    try:
        return converge(Settings.from_env(argv[1]))
    except (ConvergeError, OSError, ValueError, KeyError, urllib.error.URLError) as error:
        log(f"error: {error}")
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
