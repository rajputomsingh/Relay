import { createHmac, timingSafeEqual } from "node:crypto";

/**
 * The signing prefix used by Relay's v1 signing scheme.
 * Kept identical to the Go server's security.SignaturePrefix.
 */
const SIGNATURE_PREFIX = "v1=";

/**
 * Default freshness window. Matches security.DefaultMaxAge in the Go server.
 */
export const DEFAULT_MAX_AGE_MS = 5 * 60 * 1000; // 5 minutes

/**
 * Error thrown when a webhook signature is stale, malformed, or mismatched.
 */
export class SignatureError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SignatureError";
  }
}

/**
 * Compute the canonical signature for a given timestamp and body.
 * Exported for tests and for consumers who want to construct signed
 * requests (e.g., in a mock receiver).
 *
 * Returns "v1=" + hex(HMAC-SHA256(secret, "<timestamp>.<body>")).
 */
export function signWebhookBody(
  secret: string,
  timestamp: number,
  body: Uint8Array | string,
): string {
  const mac = createHmac("sha256", secret);
  mac.update(String(timestamp));
  mac.update(".");
  mac.update(typeof body === "string" ? Buffer.from(body, "utf8") : body);
  return SIGNATURE_PREFIX + mac.digest("hex");
}

/**
 * Verify the signature of an inbound Relay webhook.
 *
 * Two checks:
 *   1. Timestamp freshness: |now - timestamp| must be <= maxAge.
 *   2. HMAC: signature must match HMAC-SHA256(secret, "<timestamp>.<body>").
 *
 * Both must pass. Throws SignatureError on any failure; the caller
 * should reject the webhook with 401.
 *
 * @param secret          The endpoint's signing secret.
 * @param timestampHeader Value of X-Relay-Timestamp.
 * @param signatureHeader Value of X-Relay-Signature.
 * @param body            Raw request body bytes (before JSON parsing).
 * @param maxAgeMs        Freshness window in milliseconds.
 *                        Defaults to DEFAULT_MAX_AGE_MS (5 minutes).
 */
export function verifyWebhookSignature(
  secret: string,
  timestampHeader: string,
  signatureHeader: string,
  body: Uint8Array | string,
  maxAgeMs: number = DEFAULT_MAX_AGE_MS,
): void {
  if (!secret) {
    throw new SignatureError("secret is required");
  }
  if (!timestampHeader) {
    throw new SignatureError("X-Relay-Timestamp header is empty");
  }
  if (!signatureHeader) {
    throw new SignatureError("X-Relay-Signature header is empty");
  }
  if (maxAgeMs <= 0) {
    throw new SignatureError("maxAge must be positive");
  }

  const timestamp = Number.parseInt(timestampHeader, 10);
  if (!Number.isFinite(timestamp) || !Number.isInteger(timestamp)) {
    throw new SignatureError(
      `X-Relay-Timestamp header is not a valid integer: ${timestampHeader}`,
    );
  }

  const now = Math.floor(Date.now() / 1000);
  const deltaSeconds = Math.abs(now - timestamp);
  if (deltaSeconds * 1000 > maxAgeMs) {
    throw new SignatureError(
      `timestamp outside freshness window: ${deltaSeconds}s old, max ${maxAgeMs / 1000}s`,
    );
  }

  const expected = signWebhookBody(secret, timestamp, body);
  const expectedBuf = Buffer.from(expected, "utf8");
  const actualBuf = Buffer.from(signatureHeader, "utf8");
  if (
    expectedBuf.length !== actualBuf.length ||
    !timingSafeEqual(expectedBuf, actualBuf)
  ) {
    throw new SignatureError("signature mismatch");
  }
}