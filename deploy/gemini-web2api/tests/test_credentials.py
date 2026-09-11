"""Pure synthetic credential tests; run with unittest discovery from the sidecar."""

from __future__ import annotations

import base64
import hmac
import json
import traceback
import unittest
from concurrent.futures import ThreadPoolExecutor
from dataclasses import FrozenInstanceError
from typing import Final

from extension.credentials import (
    MAX_TOKEN_LENGTH,
    TOKEN_PREFIX,
    CredentialError,
    SessionCredential,
    decode_token,
    encode_token,
    resolve_credential,
)

_COOKIE: Final = "SID=synthetic-only; Arbitrary=other-synthetic-value"


def raw_token(payload: bytes) -> str:
    return "gemini-web:v1:" + base64.urlsafe_b64encode(payload).decode("ascii").rstrip(
        "="
    )


def forbidden_legacy_lookup(_presented: str) -> SessionCredential | None:
    raise AssertionError("Legacy lookup must not run")


def legacy_lookup(presented: str) -> SessionCredential | None:
    if hmac.compare_digest(presented.encode("utf-8"), b"synthetic-legacy-key"):
        return SessionCredential("SID=synthetic-legacy", 9)
    return None


class CredentialTests(unittest.TestCase):
    def test_round_trip_when_full_cookie_contains_multiple_pairs(self) -> None:
        cookie = 'SID=synthetic-a; __Secure-1PSID=synthetic-b; PREF="a=b"; OTHER='

        credential = decode_token(encode_token(cookie, 2))

        self.assertEqual((credential.cookie, credential.auth_user), (cookie, 2))

    def test_encoding_when_default_index_uses_exact_unpadded_v1_schema(self) -> None:
        expected = raw_token(b'{"cookie":"SID=synthetic-only","auth_user":0}')

        token = encode_token("SID=synthetic-only")

        self.assertEqual(token, expected)
        self.assertTrue(token.startswith(TOKEN_PREFIX))
        self.assertNotIn("=", token)

    def test_decode_when_json_field_order_differs_preserves_account(self) -> None:
        token = raw_token(b' { "auth_user": 123456789, "cookie": "CUSTOM=value" } ')

        credential = decode_token(token)

        self.assertEqual(credential, SessionCredential("CUSTOM=value", 123456789))

    def test_cookie_when_single_name_does_not_attest_authentication(self) -> None:
        credential = decode_token(encode_token("__Secure-1PSID=synthetic-only"))

        self.assertEqual(credential.auth_user, 0)

    def test_credential_when_mutated_is_frozen(self) -> None:
        credential = decode_token(encode_token(_COOKIE))

        for name, value in (("auth_user", 5), ("cookie", "replacement")):
            with self.assertRaises(FrozenInstanceError):
                setattr(credential, name, value)

    def test_credential_when_formatted_redacts_cookie(self) -> None:
        credential = SessionCredential(_COOKIE, 2)

        rendered = repr(credential)

        self.assertNotIn("synthetic", rendered)
        self.assertNotIn("cookie", rendered)

    def test_resolve_when_concurrent_preserves_each_account(self) -> None:
        accounts = (
            SessionCredential(_COOKIE, 2),
            SessionCredential("OTHER=synthetic-b", 7),
        )
        tokens = tuple(
            encode_token(account.cookie, account.auth_user) for account in accounts
        )

        with ThreadPoolExecutor(max_workers=8) as workers:
            results = tuple(
                workers.map(
                    resolve_credential, tokens * 100, [forbidden_legacy_lookup] * 200
                )
            )

        self.assertEqual(results, accounts * 100)
        self.assertIsNot(results[0], results[2])

    def test_resolve_when_exact_legacy_key_matches_returns_legacy_account(self) -> None:
        credential = resolve_credential("synthetic-legacy-key", legacy_lookup)

        self.assertEqual(credential, SessionCredential("SID=synthetic-legacy", 9))

    def test_resolve_when_missing_never_calls_legacy(self) -> None:
        for presented in (None, ""):
            with self.subTest(presented=presented):
                with self.assertRaises(CredentialError) as raised:
                    _ = resolve_credential(presented, forbidden_legacy_lookup)
                self.assertEqual(
                    (raised.exception.kind, raised.exception.status),
                    ("missing_credential", 401),
                )

    def test_resolve_when_unknown_keeps_exact_key_matching(self) -> None:
        for presented in (
            "unknown",
            _COOKIE,
            "synthetic-legacy-key ",
            " synthetic-legacy-key",
            "SYNTHETIC-LEGACY-KEY",
            "\u2603",
        ):
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = resolve_credential(presented, legacy_lookup)
                self.assertEqual(
                    (raised.exception.kind, raised.exception.status),
                    ("unknown_credential", 401),
                )

    def test_decode_when_credential_type_unknown_rejects_it(self) -> None:
        for token, status in (("", 401), (_COOKIE, 401), ("other:v1:abc", 401)):
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = decode_token(token)
                self.assertEqual(raised.exception.status, status)

    def test_resolve_when_web_format_invalid_never_calls_legacy(self) -> None:
        valid = encode_token(_COOKIE)
        tokens = (
            "gemini-web:",
            "gemini-web:v2:abc",
            "gemini-web:v1:",
            valid + "=",
            valid + "!",
            valid + "\n",
            valid + "\u2603",
            "gemini-web:v1:A",
            "gemini-web:v1:+/8",
            raw_token(b"\xff"),
            raw_token(b"{not json}"),
            raw_token(b"\xff\xfe{\x00}\x00"),
            raw_token(b"[" * 1500 + b"]" * 1500),
        )
        for token in tokens:
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = resolve_credential(token, forbidden_legacy_lookup)
                self.assertEqual(
                    (raised.exception.kind, raised.exception.status),
                    ("invalid_credential", 400),
                )

    def test_resolve_when_schema_invalid_never_calls_legacy(self) -> None:
        payloads = (
            b"{}",
            b"[]",
            b"null",
            b'"text"',
            b"1",
            b"true",
            b'{"cookie":"SID=x"}',
            b'{"auth_user":0}',
            b'{"cookie":"SID=x","auth_user":0,"extra":null}',
            b'{"cookie":"SID=x","cookie":"SID=y","auth_user":0}',
            b'{"cookie":"SID=x","auth_user":0,"auth_user":2}',
            b'{"cookie":null,"auth_user":0}',
            b'{"cookie":[],"auth_user":0}',
            b'{"cookie":true,"auth_user":0}',
            b'{"cookie":3,"auth_user":0}',
            b'{"cookie":{},"auth_user":0}',
            b'{"cookie":"","auth_user":0}',
            b'{"cookie":"   ","auth_user":0}',
        )
        for payload in payloads:
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = resolve_credential(raw_token(payload), forbidden_legacy_lookup)
                self.assertEqual(raised.exception.status, 400)

    def test_resolve_when_index_invalid_never_calls_legacy(self) -> None:
        for index in (
            b"-1",
            b"true",
            b"false",
            b"1.0",
            b'"2"',
            b"null",
            b"[]",
            b"{}",
            b"NaN",
            b"Infinity",
            b"9" * 5000,
        ):
            token = raw_token(b'{"cookie":"SID=x","auth_user":' + index + b"}")
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = resolve_credential(token, forbidden_legacy_lookup)
                self.assertEqual(raised.exception.status, 400)

    def test_encode_when_index_negative_or_boolean_rejects_it(self) -> None:
        for index in (-1, True, False):
            with self.subTest(index=index):
                with self.assertRaises(CredentialError) as raised:
                    _ = encode_token(_COOKIE, index)
                self.assertEqual(raised.exception.status, 400)

    def test_codec_when_cookie_has_control_or_invalid_header_character_rejects_it(
        self,
    ) -> None:
        for codepoint in (*range(32), *range(127, 160), 255, 256, 0x2603, 0xD800):
            cookie = "SID=synthetic" + chr(codepoint)
            token = raw_token(
                json.dumps({"cookie": cookie, "auth_user": 0}).encode("utf-8")
            )
            with self.subTest(codepoint=codepoint):
                with self.assertRaises(CredentialError) as encoded:
                    _ = encode_token(cookie)
                with self.assertRaises(CredentialError) as decoded:
                    _ = resolve_credential(token, forbidden_legacy_lookup)
                self.assertEqual(
                    (encoded.exception.status, decoded.exception.status), (400, 400)
                )

    def test_decode_when_unused_base64_bits_tampered_rejects_it(self) -> None:
        payload = b'{"cookie":"SID=x","auth_user":0}'
        token = raw_token(payload)
        alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
        tampered = token[:-1] + alphabet[alphabet.index(token[-1]) + 1]
        self.assertNotEqual(len(payload) % 3, 0)

        with self.assertRaises(CredentialError) as raised:
            _ = resolve_credential(tampered, forbidden_legacy_lookup)

        self.assertEqual(raised.exception.status, 400)

    def test_decode_when_schema_valid_mutation_is_not_a_signature_check(self) -> None:
        token = raw_token(b'{"cookie":"SID=synthetic-replaced","auth_user":8}')

        credential = resolve_credential(token, forbidden_legacy_lookup)

        self.assertEqual(credential, SessionCredential("SID=synthetic-replaced", 8))

    def test_codec_when_at_exact_total_token_limit_accepts_it(self) -> None:
        cookie = "SID=" + "x" * 24534

        token = encode_token(cookie)
        credential = decode_token(token)

        self.assertEqual(len(token), 32768)
        self.assertEqual(MAX_TOKEN_LENGTH, 32768)
        self.assertEqual(credential.cookie, cookie)

    def test_resolve_when_over_limit_rejects_before_any_lookup(self) -> None:
        for token in ("x" * 32769, "gemini-web:v1:" + "x" * 32756):
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = resolve_credential(token, forbidden_legacy_lookup)
                self.assertEqual(
                    (raised.exception.kind, raised.exception.status),
                    ("credential_too_long", 431),
                )

    def test_encode_when_encoded_cookie_exceeds_limit_rejects_it(self) -> None:
        for cookie in ("SID=" + "x" * 24535, "SID=" + "x" * 32768):
            with self.subTest():
                with self.assertRaises(CredentialError) as raised:
                    _ = encode_token(cookie)
                self.assertEqual(raised.exception.status, 431)

    def test_error_when_json_contains_sensitive_text_has_safe_rendering(self) -> None:
        token = raw_token(b'{"cookie":"SID=synthetic-sensitive",')

        with self.assertRaises(CredentialError) as raised:
            _ = decode_token(token)

        error = raised.exception
        rendered = str(error) + repr(error) + "".join(traceback.format_exception(error))
        self.assertNotIn("synthetic-sensitive", rendered)
        self.assertNotIn(token, rendered)
        self.assertEqual(error.args, ("invalid_credential", 400))
        self.assertIsNone(error.__cause__)
        self.assertTrue(error.__suppress_context__)


if __name__ == "__main__":
    _ = unittest.main()
