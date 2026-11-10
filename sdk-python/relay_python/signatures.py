"""Receiver-side webhook signature verification for the Relay SDK.

Implements the same HMAC-SHA256 signing protocol as the Relay server's
internal/security package, so receivers can verify Relay-sent webhooks
using the Python SDK without depending on Go code.
"""

from __future__ import annotations

import hashlib
import hmac

# Must match the Go server's security.SignaturePrefix.
SIGNATURE_PREFIX = "v1="

# Must match the Go server's security.DefaultMaxAge.
DEFAULT_MAX_AGE_MS = 5 * 60 * 1000  # 5 minutes


class SignatureError(Exception):
    """Raised when a webhook signature is stale, malformed, or mismatched."""


def sign_webhook_body(secret: str, timestamp: int, body: bytes | str) -> str:
    """Compute the canonical signature for a given timestamp and body.

    Returns "v1=" + hex(HMAC-SHA256(secret, "<timestamp>.<body>")).
    """
    if isinstance(body, str):
        body_bytes = body.encode("utf-8")
    else:
        body_bytes = body

    message = str(timestamp).encode("utf-8") + b"." + body_bytes
    mac = hmac.new(secret.encode("utf-8"), message, hashlib.sha256)
    return SIGNATURE_PREFIX + mac.hexdigest()


def verify_webhook_signature(
    secret: str,
    timestamp_header: str,
    signature_header: str,
    body: bytes | str,
    max_age_ms: int = DEFAULT_MAX_AGE_MS,
) -> None:
    """Verify the signature of an inbound Relay webhook.

    Two checks:
      1. Timestamp freshness: |now - timestamp| must be <= max_age_ms.
      2. HMAC: signature must match HMAC-SHA256(secret, "<timestamp>.<body>").

    Both must pass. Raises SignatureError on any failure; the caller
    should reject the webhook with 401.
    """
    if not secret:
        raise SignatureError("secret is required")
    if not timestamp_header:
        raise SignatureError("X-Relay-Timestamp header is empty")
    if not signature_header:
        raise SignatureError("X-Relay-Signature header is empty")
    if max_age_ms <= 0:
        raise SignatureError("max_age_ms must be positive")

    try:
        timestamp = int(timestamp_header)
    except ValueError:
        raise SignatureError(f"X-Relay-Timestamp header is not a valid integer: {timestamp_header}") from None

    import time

    now = int(time.time())
    delta_seconds = abs(now - timestamp)
    if delta_seconds * 1000 > max_age_ms:
        raise SignatureError(f"timestamp outside freshness window: {delta_seconds}s old, max {max_age_ms // 1000}s")

    expected = sign_webhook_body(secret, timestamp, body)
    if not hmac.compare_digest(expected, signature_header):
        raise SignatureError("signature mismatch")
