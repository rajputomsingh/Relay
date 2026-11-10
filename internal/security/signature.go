// Package security implements outbound webhook signing.
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// SignaturePrefix identifies the signing scheme so future versions can
	// coexist with v1 receivers.
	SignaturePrefix = "v1="

	// TimestampHeader carries the Unix timestamp used in the canonical message.
	TimestampHeader = "X-Relay-Timestamp"

	// SignatureHeader carries the hex-encoded HMAC-SHA256 digest.
	SignatureHeader = "X-Relay-Signature"

	// DefaultMaxAge is the maximum age of a timestamp that VerifyWithFreshness
	// will accept. Receivers who need a different window should pass one
	// explicitly. Five minutes is standard for webhook signatures (Stripe,
	// GitHub, and others use the same or shorter).
	DefaultMaxAge = 5 * time.Minute
)

// ErrInvalidSignature is returned when a supplied signature cannot be verified.
var ErrInvalidSignature = errors.New("invalid webhook signature")

// ErrStaleTimestamp is returned when the supplied timestamp is outside the
// acceptable freshness window.
var ErrStaleTimestamp = errors.New("webhook signature timestamp out of acceptable range")

// Signer produces HMAC-SHA256 signatures over webhook request bodies.
type Signer struct {
	secret []byte
}

// NewSigner creates a Signer. The secret must not be empty.
func NewSigner(secret string) (*Signer, error) {
	if secret == "" {
		return nil, errors.New("signing secret must not be empty")
	}
	return &Signer{secret: []byte(secret)}, nil
}

// SignBody returns the canonical signature value for the given timestamp and
// body. The returned string includes the version prefix, e.g. "v1=abc...".
func (s *Signer) SignBody(timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a supplied signature against the canonical message.
// Returns nil on success, ErrInvalidSignature on mismatch, and a descriptive
// error on malformed input.
//
// Verify does NOT enforce a timestamp freshness window. A valid signature
// with an ancient timestamp will pass. Receivers who want replay protection
// must enforce freshness — either by inspecting the timestamp header
// themselves, or by calling VerifyWithFreshness instead of Verify.
func (s *Signer) Verify(timestamp int64, body []byte, signature string) error {
	if !strings.HasPrefix(signature, SignaturePrefix) {
		return errors.New("signature is missing version prefix")
	}

	expected := s.SignBody(timestamp, body)

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyWithFreshness is Verify plus a timestamp freshness check. It is
// the recommended entry point for webhook receivers.
//
// The freshness window is symmetric: |now - timestamp| must be <= maxAge.
// A negative maxAge is rejected as a configuration error.
//
// Returns:
//   - nil on valid, fresh signature
//   - ErrInvalidSignature on signature mismatch
//   - ErrStaleTimestamp if the timestamp is outside the window
//   - a descriptive error on malformed input
func (s *Signer) VerifyWithFreshness(
	timestamp int64,
	body []byte,
	signature string,
	maxAge time.Duration,
) error {
	if maxAge <= 0 {
		return fmt.Errorf("maxAge must be positive, got %s", maxAge)
	}

	// Check freshness before signature — prevents an attacker who can
	// cause a stale but otherwise valid signature to waste HMAC cycles.
	now := time.Now().UTC().Unix()
	delta := now - timestamp
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > maxAge {
		return ErrStaleTimestamp
	}

	return s.Verify(timestamp, body, signature)
}

// TimestampNow returns the current Unix timestamp used in signatures.
func TimestampNow() int64 {
	return time.Now().UTC().Unix()
}
