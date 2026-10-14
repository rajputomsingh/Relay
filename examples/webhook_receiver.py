"""
Relay signed webhook receiver example.

Verifies:
- X-Relay-Timestamp freshness (default 5-minute tolerance)
- X-Relay-Signature as HMAC-SHA256(secret, timestamp + "." + raw_body)
- Constant-time signature comparison

Run:
    set RELAY_WEBHOOK_SIGNING_SECRET=0123456789abcdef0123456789abcdef
    python webhook_receiver.py
"""

import hashlib
import hmac
import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer
from time import time

# Must match Relay's RELAY_WEBHOOK_SIGNING_SECRET.
SIGNING_SECRET = os.environ.get("RELAY_WEBHOOK_SIGNING_SECRET", "").encode()

# Reject requests older than this many seconds.
TIMESTAMP_TOLERANCE_SECONDS = 300

# Max body size to read from the socket.
MAX_BODY_BYTES = 1 << 20

SIGNATURE_PREFIX = "v1="


class WebhookHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        # ---------------------------------------------------------
        # 1. Read the raw body first. The signature is over bytes,
        #    not over a parsed object. Parsing and re-serializing
        #    JSON before verification would break the signature.
        # ---------------------------------------------------------
        length = int(self.headers.get("Content-Length", 0))
        if length <= 0 or length > MAX_BODY_BYTES:
            self.respond(400, {"error": "invalid content length"})
            return

        body = self.rfile.read(length)

        # ---------------------------------------------------------
        # 2. If signing is configured, verify before doing anything.
        # ---------------------------------------------------------
        if SIGNING_SECRET:
            error = self.verify_signature(body)
            if error is not None:
                print(f"REJECTED: {error}")
                self.respond(401, {"error": error})
                return

        # ---------------------------------------------------------
        # 3. Only now parse JSON and handle the event.
        # ---------------------------------------------------------
        try:
            payload = json.loads(body)
        except json.JSONDecodeError:
            self.respond(400, {"error": "invalid JSON"})
            return

        print("\n--- WEBHOOK RECEIVED ---")
        print("Path:", self.path)
        print("Event-ID:", self.headers.get("X-Relay-Event-ID"))
        print("Timestamp:", self.headers.get("X-Relay-Timestamp"))
        print("Signature:", self.headers.get("X-Relay-Signature"))
        print("Idempotency-Key:", self.headers.get("Idempotency-Key"))
        print("Payload:", json.dumps(payload, indent=2))

        self.respond(200, {"received": True})

    def verify_signature(self, body: bytes):
        ts_header = self.headers.get("X-Relay-Timestamp", "")
        sig_header = self.headers.get("X-Relay-Signature", "")

        if not ts_header or not sig_header:
            return "missing signature headers"

        try:
            timestamp = int(ts_header)
        except ValueError:
            return "invalid timestamp"

        # Reject stale requests.
        skew = abs(int(time()) - timestamp)
        if skew > TIMESTAMP_TOLERANCE_SECONDS:
            return f"stale request (skew {skew}s)"

        # Signature must be prefixed to identify the scheme.
        if not sig_header.startswith(SIGNATURE_PREFIX):
            return "malformed signature prefix"

        # Compute the expected signature over the exact bytes received.
        message = ts_header.encode() + b"." + body
        expected = SIGNATURE_PREFIX + hmac.new(
            SIGNING_SECRET,
            message,
            hashlib.sha256,
        ).hexdigest()

        # Constant-time comparison.
        if not hmac.compare_digest(expected, sig_header):
            return "invalid signature"

        return None

    def respond(self, status: int, payload: dict):
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args):
        # Keep the console clean; our own prints carry more information.
        return


if __name__ == "__main__":
    if not SIGNING_SECRET:
        print(
            "WARNING: RELAY_WEBHOOK_SIGNING_SECRET not set.\n"
            "Receiver will accept unsigned requests.\n"
            "Set it to the same value as Relay to enable verification.\n"
        )
    elif len(SIGNING_SECRET) < 32:
        print(
            "WARNING: RELAY_WEBHOOK_SIGNING_SECRET is shorter than 32 bytes.\n"
        )

    print("Receiver listening on http://127.0.0.1:9090")
    HTTPServer(("127.0.0.1", 9090), WebhookHandler).serve_forever()