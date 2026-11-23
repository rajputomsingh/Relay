package auth

import (
	"net/http"
	"strings"

	"github.com/rajputomsingh/relay/internal/tenant"
)

// Middleware authenticates every request except those whose path is in
// public, resolves the presented key to a TenantID, and injects it into
// the request context. Tenant identity is ONLY ever set from the
// resolver - never from headers, body, or query params.
//
// Public entries ending in "/" are treated as path prefixes; all others
// are exact matches.
func Middleware(r Resolver, public []string) func(http.Handler) http.Handler {
	pub := make(map[string]struct{}, len(public))
	for _, p := range public {
		pub[p] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if isPublic(req.URL.Path, pub) {
				next.ServeHTTP(w, req)
				return
			}
			key, ok := bearer(req.Header.Get("Authorization"))
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			tid, err := r.Resolve(req.Context(), key)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := tenant.WithTenant(req.Context(), tid)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func isPublic(path string, pub map[string]struct{}) bool {
	if _, ok := pub[path]; ok {
		return true
	}
	for p := range pub {
		if strings.HasSuffix(p, "/") && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func bearer(h string) (string, bool) {
	const p = "Bearer "
	if len(h) <= len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	return h[len(p):], true
}
