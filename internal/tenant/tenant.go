package tenant

import "context"

// TenantID is a typed identifier for a tenant. Never a bare string.
type TenantID string

type ctxKey struct{} // unexported, collision-proof

// WithTenant returns a child context carrying tid.
func WithTenant(ctx context.Context, tid TenantID) context.Context {
	return context.WithValue(ctx, ctxKey{}, tid)
}

// FromContext returns the tenant ID and whether one was present.
func FromContext(ctx context.Context) (TenantID, bool) {
	tid, ok := ctx.Value(ctxKey{}).(TenantID)
	if !ok || tid == "" {
		return "", false
	}
	return tid, true
}

// MustFromContext is for callers that have already enforced auth.
// Panics only on programmer error, never on user input.
func MustFromContext(ctx context.Context) TenantID {
	tid, ok := FromContext(ctx)
	if !ok {
		panic("tenant: no tenant in context - auth middleware not applied")
	}
	return tid
}
