package security

import (
	"errors"
	"strings"
	"testing"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func mustSigner(t *testing.T) *Signer {
	t.Helper()
	signer, err := NewSigner(testSecret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer
}

func TestNewSignerRejectsEmptySecret(t *testing.T) {
	if _, err := NewSigner(""); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestSignBodyDeterministic(t *testing.T) {
	signer := mustSigner(t)
	body := []byte(`{"event_id":"abc"}`)

	first := signer.SignBody(1792000000, body)
	second := signer.SignBody(1792000000, body)

	if first != second {
		t.Fatalf("signature not deterministic: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, SignaturePrefix) {
		t.Fatalf("signature missing prefix: %q", first)
	}
}

func TestVerifyAcceptsValidSignature(t *testing.T) {
	signer := mustSigner(t)
	body := []byte(`{"event_id":"abc"}`)
	ts := int64(1792000000)

	sig := signer.SignBody(ts, body)
	if err := signer.Verify(ts, body, sig); err != nil {
		t.Fatalf("Verify rejected a valid signature: %v", err)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	signer := mustSigner(t)
	ts := int64(1792000000)
	body := []byte(`{"event_id":"abc"}`)
	sig := signer.SignBody(ts, body)

	tampered := []byte(`{"event_id":"xyz"}`)
	err := signer.Verify(ts, tampered, sig)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	signer := mustSigner(t)
	other, _ := NewSigner("ffffffffffffffffffffffffffffffff")
	ts := int64(1792000000)
	body := []byte(`{"event_id":"abc"}`)

	sig := other.SignBody(ts, body)
	if err := signer.Verify(ts, body, sig); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestVerifyRejectsStaleTimestamp(t *testing.T) {
	signer := mustSigner(t)
	body := []byte(`{"event_id":"abc"}`)

	sig := signer.SignBody(1792000000, body)
	err := signer.Verify(1792000001, body, sig)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestVerifyRejectsMalformedSignature(t *testing.T) {
	signer := mustSigner(t)
	body := []byte(`{"event_id":"abc"}`)

	cases := []string{"", "abc", "deadbeef", "v2=deadbeef"}
	for _, sig := range cases {
		err := signer.Verify(1792000000, body, sig)
		if err == nil {
			t.Errorf("expected error for signature %q", sig)
		}
	}
}

func TestVerifyHandlesEmptyBody(t *testing.T) {
	signer := mustSigner(t)
	ts := int64(1792000000)

	sig := signer.SignBody(ts, nil)
	if err := signer.Verify(ts, nil, sig); err != nil {
		t.Fatalf("Verify rejected an empty body signature: %v", err)
	}
}
