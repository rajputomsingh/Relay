package tenant_test

import (
	"context"
	"testing"

	"github.com/rajputomsingh/relay/internal/tenant"
)

func TestWithFromContext(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), "t1")
	got, ok := tenant.FromContext(ctx)
	if !ok || got != "t1" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestFromContext_Missing(t *testing.T) {
	if _, ok := tenant.FromContext(context.Background()); ok {
		t.Fatal("expected no tenant")
	}
}

func TestFromContext_EmptyIsMissing(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), "")
	if _, ok := tenant.FromContext(ctx); ok {
		t.Fatal("empty tenant must be treated as missing")
	}
}

func TestMustFromContext_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on missing tenant")
		}
	}()
	tenant.MustFromContext(context.Background())
}
