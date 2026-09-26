import importlib.util
import json
import os
from pathlib import Path
import sys
from typing import TYPE_CHECKING, Protocol, cast, runtime_checkable

import pytest
import yaml

if TYPE_CHECKING:
    from .stage_parsing import DocumentShapeError, decode, is_config, is_flow, is_originals, is_receipt, narrow
else:
    from stage_parsing import DocumentShapeError, decode, is_config, is_flow, is_originals, is_receipt, narrow


@runtime_checkable
class Importer(Protocol):
    ROOT: Path

    def intake(self, path: Path) -> None: ...


@pytest.fixture
def importer(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Importer:
    spec = importlib.util.spec_from_file_location("local_importer_test", Path(__file__).with_name("import-stage.py"))
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, spec.name, module)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, "ROOT", tmp_path)
    for directory in ("intake", "receipts", "originals"):
        (tmp_path / directory).mkdir()
    _ = (tmp_path / "originals/mappings.json").write_text("{}")
    original_stat = Path.stat

    def root_stat(path: Path, *, follow_symlinks: bool = True) -> os.stat_result:
        current = original_stat(path, follow_symlinks=follow_symlinks)
        return os.stat_result((current.st_mode, current.st_ino, current.st_dev, current.st_nlink, 0, current.st_gid, current.st_size, current.st_atime, current.st_mtime, current.st_ctime))

    monkeypatch.setattr(Path, "stat", root_stat)
    assert isinstance(module, Importer)
    return module


@pytest.mark.parametrize("malformed", [{"label": 123}, {"auth_user": True}, {"consent": "true"}])
def test_malformed_second_capture_is_rejected_before_login(
    monkeypatch: pytest.MonkeyPatch, importer: Importer, malformed: dict[str, object],
) -> None:
    tmp_path = importer.ROOT
    capture = {"existing_id": "", "label": "Synthetic account", "token": "gemini-web:v1:synthetic", "account_sha256": "a" * 64, "auth_user": 0, "validated": True, "consent": True}
    bundle = tmp_path / "intake/bundle.json"
    _ = bundle.write_text(json.dumps({"version": 1, "bundle_id": "b" * 32, "consent": True, "accounts": [capture, {**capture, **malformed}]}))
    bundle.chmod(0o600)
    calls: list[str] = []

    def unexpected_login(name: str, _body: str) -> str:
        calls.append(name)
        raise RuntimeError("unexpected_synthetic_login")

    monkeypatch.setattr(importer, "operation", unexpected_login)
    with pytest.raises(DocumentShapeError, match="^invalid_document_structure$"):
        importer.intake(bundle)
    assert calls == []
    assert list((tmp_path / "receipts").iterdir()) == []


def test_config_validation_preserves_unknown_fields_and_optional_index() -> None:
    source = {"port": 8317, "plugins": {"configs": {"other-provider": {"enabled": True}, "gemini-web": {"maintenance_sources": {"synthetic.json": {"token_ref": "reference-only", "profile_guid": "synthetic-profile", "expected_gaia_sha256": "a" * 64}}}}}, "extra": {"large_integer": 9007199254740993, "values": [False, None]}}
    raw = json.dumps(source)
    parsed = decode(raw, is_config)
    yaml_parsed = narrow(cast(object, yaml.safe_load(raw)), is_config)
    assert json.dumps(parsed) == raw
    assert json.dumps(yaml_parsed) == raw
    assert "auth_user" not in parsed["plugins"]["configs"]["gemini-web"]["maintenance_sources"]["synthetic.json"]


@pytest.mark.parametrize("raw", [
    '{"plugins": {"configs": {"gemini-web": {"maintenance_sources": []}}}}',
    '{"plugins": {"configs": {"gemini-web": {"maintenance_sources": {"synthetic.json": {"token_ref": "ref", "profile_guid": "profile", "expected_gaia_sha256": "digest", "auth_user": false}}}}}}',
])
def test_malformed_config_is_rejected_at_yaml_boundary(raw: str) -> None:
    with pytest.raises(DocumentShapeError):
        _ = narrow(cast(object, yaml.safe_load(raw)), is_config)


@pytest.mark.parametrize("status", ["pending", "cancelled", "saved", "error"])
def test_login_response_preserves_optional_fields(status: str) -> None:
    raw = json.dumps({"state": "c" * 64, "status": status, "models_ready": False})
    assert json.dumps(decode(raw, is_flow)) == raw


def test_mapping_without_embedded_auth_record_remains_valid() -> None:
    raw = json.dumps({"synthetic.json": {"binding": {"token_ref": "reference-only", "profile_guid": "synthetic-profile", "expected_gaia_sha256": "a" * 64}, "disabled": True}})
    assert json.dumps(decode(raw, is_originals)) == raw


def test_valid_import_keeps_existing_id_disabled_flag_and_receipt(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, importer: Importer,
) -> None:
    account_id = "gemini-web-synthetic.json"
    label = "Synthetic account"
    binding = {"token_ref": "reference-only", "profile_guid": "synthetic-profile", "expected_gaia_sha256": "a" * 64, "auth_user": 0}
    stored = {"id": account_id, "label": label, "token_ref": "session://gemini-web/" + "d" * 32, "disabled": True}
    _ = (tmp_path / "originals/mappings.json").write_text(json.dumps({account_id: {"record": {**stored, "token_ref": "reference-only"}, "binding": binding, "disabled": False}}))
    _ = (tmp_path / "config.yaml").write_text(json.dumps({"plugins": {"configs": {"gemini-web": {"maintenance_sources": {account_id: binding}}}}}))
    (tmp_path / "auths").mkdir()
    capture = {"existing_id": account_id, "label": label, "token": "gemini-web:v1:synthetic", "account_sha256": "a" * 64, "auth_user": 0, "validated": True, "consent": True}
    bundle = tmp_path / "intake/bundle.json"
    _ = bundle.write_text(json.dumps({"version": 1, "bundle_id": "b" * 32, "consent": True, "accounts": [capture]}))
    bundle.chmod(0o600)
    calls: list[str] = []

    def synthetic_login(name: str, _body: str) -> str:
        calls.append(name)
        if name == "complete":
            _ = (tmp_path / "auths" / account_id).write_text(json.dumps(stored))
        return json.dumps({"state": "c" * 64, "status": "saved" if name == "complete" else "pending", "models_ready": False})

    monkeypatch.setattr(importer, "operation", synthetic_login)
    importer.intake(bundle)
    receipt = decode((tmp_path / "receipts" / account_id).read_text(), is_receipt)
    assert calls == ["start", "complete"]
    assert receipt == {"account_id": account_id, "state": "c" * 64, "phase": "saved"}
