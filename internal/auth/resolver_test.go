package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rajputomsingh/relay/internal/auth"
)

func TestLegacyResolver_Valid(t *testing.T) {
	r := auth.LegacyResolver{Key: "secret", Tenant: "legacy"}
	tid, err := r.Resolve(context.Background(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if tid != "legacy" {
		t.Fatalf("got %q", tid)
	}
}

func TestLegacyResolver_Invalid(t *testing.T) {
	r := auth.LegacyResolver{Key: "secret", Tenant: "legacy"}
	if _, err := r.Resolve(context.Background(), "wrong"); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey, got %v", err)
	}
}

func TestLegacyResolver_EmptyConfiguredKey(t *testing.T) {
	r := auth.LegacyResolver{Key: "", Tenant: "legacy"}
	if _, err := r.Resolve(context.Background(), "anything"); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey, got %v", err)
	}
}

func TestLegacyResolver_UnresolvedTenant(t *testing.T) {
	r := auth.LegacyResolver{Key: "secret", Tenant: ""}
	if _, err := r.Resolve(context.Background(), "secret"); !errors.Is(err, auth.ErrUnresolvedTen) {
		t.Fatalf("want ErrUnresolvedTen, got %v", err)
	}
}

func TestChainResolver_FirstWins(t *testing.T) {
	c := auth.ChainResolver{
		auth.LegacyResolver{Key: "secret", Tenant: "legacy"},
	}
	tid, err := c.Resolve(context.Background(), "secret")
	if err != nil || tid != "legacy" {
		t.Fatalf("got %q %v", tid, err)
	}
}

func TestChainResolver_AllFail(t *testing.T) {
	c := auth.ChainResolver{
		auth.LegacyResolver{Key: "a", Tenant: "t"},
	}
	if _, err := c.Resolve(context.Background(), "b"); err == nil {
		t.Fatal("expected error")
	}
}
