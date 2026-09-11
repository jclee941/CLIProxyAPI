from __future__ import annotations

import hashlib
import json
from dataclasses import asdict

import pytest

from extension.account import AccountError, JsonValue
from tests.test_account import provider

GAIA = "123456789012345678901"
DIGEST = hashlib.sha256(GAIA.encode("utf-8")).hexdigest()


@pytest.mark.parametrize("identity", [GAIA, 123456789012345678901, None, True, "1" * 20, "1" * 22, "x" * 21, "\u0661" * 21])
def test_identity_when_three_wiz_fields_are_equal(identity: JsonValue) -> None:
    with provider() as (server, session):
        server.html = "<script>window.WIZ_global_data=" + json.dumps({
            "SNlM0e": "xsrf", "cfb2h": "build",
            "S06Grb": identity, "W3Yyqf": identity, "qDCSke": identity,
        }) + ";</script>"

        models = session.account_models()

        assert models.account_sha256 == (DIGEST if identity == GAIA else None)
        assert GAIA not in json.dumps(asdict(models))


@pytest.mark.parametrize("field", ["S06Grb", "W3Yyqf", "qDCSke"])
@pytest.mark.parametrize("replacement", [None, "987654321098765432109", 123456789012345678901])
def test_identity_when_one_field_is_missing_mismatched_or_numeric(field: str, replacement: JsonValue) -> None:
    values: dict[str, JsonValue] = {"SNlM0e": "xsrf", "cfb2h": "build", "S06Grb": GAIA, "W3Yyqf": GAIA, "qDCSke": GAIA}
    if replacement is None:
        del values[field]
    else:
        values[field] = replacement
    with provider() as (server, session):
        server.html = "<script>window.WIZ_global_data=" + json.dumps(values) + ";</script>"

        models = session.account_models()

        assert models.account_sha256 is None


@pytest.mark.parametrize("page,kind", [
    ('<a href="https://accounts.google.com/ServiceLogin?continue=https://gemini.google.com/app">Sign in</a>', "unauthenticated"),
    ('<form action="https://accounts.google.com/v3/signin/identifier"></form>', "unauthenticated"),
    ('<form id="gaia_loginform" action="/ServiceLoginAuth"></form>', "unauthenticated"),
    ('<html>ServiceLogin</html>', "bootstrap_failed"),
    ('<script>const text="https://accounts.google.com/ServiceLogin";</script>', "bootstrap_failed"),
    ('<a href="https://evil.invalid/ServiceLogin">Sign in</a>', "bootstrap_failed"),
    ('<html>unknown provider page</html>', "bootstrap_failed"),
])
def test_bootstrap_when_login_marker_is_explicit(page: str, kind: str) -> None:
    with provider() as (server, session):
        server.html = page

        with pytest.raises(AccountError) as caught:
            session.bootstrap()

        assert caught.value.kind == kind
        assert len(server.requests) == 1


def test_bootstrap_when_authenticated_page_also_links_to_signin() -> None:
    with provider() as (server, session):
        server.html += '<a href="https://accounts.google.com/ServiceLogin">Switch</a>'

        models = session.account_models()

        assert models.available
        assert models.account_sha256 is None


def test_bootstrap_when_redirect_requires_signin() -> None:
    with provider() as (server, session):
        server.http_status = 302
        server.location = "https://accounts.google.com/ServiceLogin?continue=https://gemini.google.com/app"

        with pytest.raises(AccountError) as caught:
            session.bootstrap()

        assert caught.value.kind == "unauthenticated"
        assert len(server.requests) == 1


@pytest.mark.parametrize("suffix", [
    ',"S06Grb":"987654321098765432109"',
    ',"S06Grb":123456789012345678901',
])
def test_identity_when_wiz_keys_are_duplicated_is_unverified(suffix: str) -> None:
    with provider() as (server, session):
        server.html = '<script>window.WIZ_global_data={"SNlM0e":"xsrf","cfb2h":"build",' + ','.join('"' + key + '":"' + GAIA + '"' for key in ("S06Grb", "W3Yyqf", "qDCSke")) + suffix + '};</script>'

        models = session.account_models()

        assert models.account_sha256 is None


def test_identity_when_bootstrap_changes_clears_previous_hash() -> None:
    with provider() as (server, session):
        server.html = 'window.WIZ_global_data=' + json.dumps({"SNlM0e": "xsrf", "cfb2h": "build", "S06Grb": GAIA, "W3Yyqf": GAIA, "qDCSke": GAIA})
        assert session.account_models().account_sha256 == DIGEST
        server.html = '{"SNlM0e":"xsrf","cfb2h":"build"}'

        session.bootstrap()

        assert session.account_models().account_sha256 is None
