# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Run: python3 snapshot-profile.py SOURCE_PROFILE NEW_PRIVATE_DESTINATION.

Copies a stable file set without opening or changing the source databases.
SQLite then recovers the private copy normally, including its WAL/journal.
Only Google cookies and native account-token rows survive the export.
"""

from contextlib import closing
from pathlib import Path
import json
import os
import sqlite3
import sys
from tempfile import TemporaryDirectory
from typing import Final


FILES: Final = (
    "Preferences", "Secure Preferences", "Google Profile Picture.png",
    "Cookies", "Cookies-wal", "Cookies-journal",
    "Network/Cookies", "Network/Cookies-wal", "Network/Cookies-journal",
    "Web Data", "Web Data-wal", "Web Data-journal",
)


class SnapshotError(Exception):
    """The source changed or the exported database is inconsistent."""


def stamp(path: Path) -> tuple[int, int, int, int] | None:
    try:
        value = path.stat()
    except FileNotFoundError:
        return None
    return value.st_ino, value.st_size, value.st_mtime_ns, value.st_ctime_ns


def snapshot(source: Path, destination: Path) -> None:
    if destination.exists():
        raise SnapshotError("destination_exists")
    paths = [source / name for name in FILES]
    paths.append(source.parent / "Local State")
    before = [stamp(path) for path in paths]
    contents = [path.read_bytes() if version is not None else None for path, version in zip(paths, before)]
    if before != [stamp(path) for path in paths]:
        raise SnapshotError("source_changed_during_snapshot")
    if contents != [path.read_bytes() if version is not None else None for path, version in zip(paths, before)]:
        raise SnapshotError("source_changed_during_verification")
    if before != [stamp(path) for path in paths]:
        raise SnapshotError("source_changed_after_verification")
    destination.mkdir(mode=0o700)
    with TemporaryDirectory(dir=destination, prefix="sqlite-private-") as temporary:
        work = Path(temporary)
        for name, payload in zip((*FILES, "Local State"), contents):
            if payload is not None:
                target = work / name
                target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                _ = target.write_bytes(payload)
        for name in ("Cookies", "Network/Cookies", "Web Data"):
            database = work / name
            if not database.exists():
                continue
            with closing(sqlite3.connect(database)) as recovered:
                if recovered.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
                    raise SnapshotError("sqlite_integrity_failed")
                with closing(sqlite3.connect(":memory:")) as clean:
                    recovered.backup(clean)
                    tables: list[tuple[str]] = clean.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()
                    for (table,) in tables:
                        if table.startswith("sqlite_") or table == "meta":
                            continue
                        quoted = '"' + table.replace('"', '""') + '"'
                        if name.endswith("Cookies") and table == "cookies":
                            _ = clean.execute("DELETE FROM cookies WHERE NOT (host_key IN ('google.com', '.google.com') OR host_key LIKE '%.google.com')")
                        elif name == "Web Data" and table == "token_service":
                            continue
                        else:
                            _ = clean.execute("DELETE FROM " + quoted)
                    clean.commit()
                    _ = clean.execute("VACUUM")
                    target = destination / name
                    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                    with closing(sqlite3.connect(target)) as exported:
                        clean.backup(exported)
                        if exported.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
                            raise SnapshotError("export_integrity_failed")
        for name in ("Preferences", "Secure Preferences", "Google Profile Picture.png", "Local State"):
            source_file = work / name
            if source_file.exists():
                if name != "Google Profile Picture.png":
                    json.loads(source_file.read_bytes())
                _ = (destination / name).write_bytes(source_file.read_bytes())
    print("stable_snapshot_exported; sqlite_integrity_ok; google_cookies_and_native_tokens_only")


if __name__ == "__main__":
    _ = os.umask(0o077)
    if len(sys.argv) != 3:
        raise SystemExit("usage: snapshot-profile.py SOURCE_PROFILE NEW_PRIVATE_DESTINATION")
    try:
        snapshot(Path(sys.argv[1]).resolve(strict=True), Path(sys.argv[2]))
    except (SnapshotError, sqlite3.Error, OSError, json.JSONDecodeError) as error:
        raise SystemExit("snapshot_failed:" + type(error).__name__) from None
