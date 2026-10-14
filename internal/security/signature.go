// Package security implements outbound webhook signing.
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
)

// ErrInvalidSignature is returned when a supplied signature cannot be verified.
var ErrInvalidSignature = errors.New("invalid webhook signature")

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

// TimestampNow returns the current Unix timestamp used in signatures.
func TimestampNow() int64 {
	return time.Now().UTC().Unix()
}
