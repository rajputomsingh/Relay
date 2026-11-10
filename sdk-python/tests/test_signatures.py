"""Signature verification tests for the Relay Python SDK."""

from __future__ import annotations

import time

import pytest

from relay_python import (
    DEFAULT_MAX_AGE_MS,
    SignatureError,
    sign_webhook_body,
    verify_webhook_signature,
)

SECRET = "0123456789abcdef0123456789abcdef"
BODY = b'{"event_id":"abc"}'


def test_verify_accepts_valid_fresh_signature() -> None:
    ts = int(time.time())
    sig = sign_webhook_body(SECRET, ts, BODY)
    verify_webhook_signature(SECRET, str(ts), sig, BODY)


def test_verify_rejects_tampered_body() -> None:
    ts = int(time.time())
    sig = sign_webhook_body(SECRET, ts, BODY)
    with pytest.raises(SignatureError, match="signature mismatch"):
        verify_webhook_signature(SECRET, str(ts), sig, b'{"event_id":"xyz"}')


def test_verify_rejects_wrong_secret() -> None:
    ts = int(time.time())
    sig = sign_webhook_body(SECRET, ts, BODY)
    with pytest.raises(SignatureError, match="signature mismatch"):
        verify_webhook_signature("ffffffffffffffffffffffffffffffff", str(ts), sig, BODY)


def test_verify_rejects_stale_timestamp() -> None:
    ts = int(time.time()) - 600
    sig = sign_webhook_body(SECRET, ts, BODY)
    with pytest.raises(SignatureError, match="freshness window"):
        verify_webhook_signature(SECRET, str(ts), sig, BODY)


def test_verify_rejects_future_timestamp() -> None:
    ts = int(time.time()) + 600
    sig = sign_webhook_body(SECRET, ts, BODY)
    with pytest.raises(SignatureError, match="freshness window"):
        verify_webhook_signature(SECRET, str(ts), sig, BODY)


def test_verify_rejects_empty_secret() -> None:
    with pytest.raises(SignatureError, match="secret is required"):
        verify_webhook_signature("", "123", "v1=abc", BODY)


def test_verify_rejects_malformed_timestamp() -> None:
    with pytest.raises(SignatureError, match="not a valid integer"):
        verify_webhook_signature(SECRET, "not-a-number", "v1=abc", BODY)


def test_default_max_age_is_five_minutes() -> None:
    assert DEFAULT_MAX_AGE_MS == 5 * 60 * 1000
