package relay

import (
	"fmt"
	"time"

	"github.com/rajputomsingh/relay/internal/security"
)

// VerifyWebhookSignature verifies the signature of an inbound webhook
// received from Relay.
//
// This is the receiver-side helper. It performs two checks:
//
//  1. Timestamp freshness: |now - timestamp| must be <= maxAge. This
//     prevents replay of previously captured signed requests.
//  2. HMAC: the signature must match HMAC-SHA256(secret, timestamp + "." + body).
//
// Both checks must pass. Any failure returns a non-nil error; the caller
// should reject the webhook.
//
// The parameters correspond to the HTTP headers and body the receiver
// receives:
//
//   - secret: the shared secret configured on the endpoint
//   - timestampHeader: value of X-Relay-Timestamp
//   - signatureHeader: value of X-Relay-Signature
//   - body: raw request body bytes (before any JSON unmarshaling)
//   - maxAge: freshness window, e.g. security.DefaultMaxAge (5m)
//
// Example usage in an HTTP handler:
//
//	if err := relay.VerifyWebhookSignature(
//	    endpointSecret,
//	    r.Header.Get("X-Relay-Timestamp"),
//	    r.Header.Get("X-Relay-Signature"),
//	    bodyBytes,
//	    5*time.Minute,
//	); err != nil {
//	    http.Error(w, "invalid signature", http.StatusUnauthorized)
//	    return
//	}
func VerifyWebhookSignature(
	secret, timestampHeader, signatureHeader string, body []byte, maxAge time.Duration,
) error {
	if secret == "" {
		return fmt.Errorf("verify webhook signature: secret is required")
	}
	if timestampHeader == "" {
		return fmt.Errorf("verify webhook signature: timestamp header is empty")
	}
	if signatureHeader == "" {
		return fmt.Errorf("verify webhook signature: signature header is empty")
	}
	if maxAge <= 0 {
		return fmt.Errorf("verify webhook signature: maxAge must be positive")
	}

	signer, err := security.NewSigner(secret)
	if err != nil {
		return fmt.Errorf("verify webhook signature: %w", err)
	}

	var timestamp int64
	if _, err := fmt.Sscan(timestampHeader, &timestamp); err != nil {
		return fmt.Errorf(
			"verify webhook signature: malformed timestamp %q: %w",
			timestampHeader, err,
		)
	}

	return signer.VerifyWithFreshness(timestamp, body, signatureHeader, maxAge)
}

// SignWebhookBody signs a webhook body for testing or for consumers who
// want to construct signed requests. Most receivers only need
// VerifyWebhookSignature.
//
// Returns the timestamp header value and signature header value that
// would be sent by Relay, ready to be placed in X-Relay-Timestamp and
// X-Relay-Signature.
func SignWebhookBody(secret string, body []byte) (timestamp, signature string, err error) {
	signer, err := security.NewSigner(secret)
	if err != nil {
		return "", "", err
	}

	ts := security.TimestampNow()
	sig := signer.SignBody(ts, body)

	return fmt.Sprintf("%d", ts), sig, nil
}
