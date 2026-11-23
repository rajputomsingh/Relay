package auth_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rajputomsingh/relay/internal/auth"
	"github.com/rajputomsingh/relay/internal/tenant"
)

func TestMiddleware_MissingAuth(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "t"}, nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not be reached")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/events", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestMiddleware_InvalidKey(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "t"}, nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not be reached")
	}))
	req := httptest.NewRequest("GET", "/v1/events", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestMiddleware_PublicPassthrough(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "t"}, []string{"/v1/health"})
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := tenant.FromContext(r.Context()); ok {
			t.Error("public route must not have tenant")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/health", nil))
	if !called {
		t.Fatal("handler not called")
	}
}

func TestMiddleware_PublicPrefixPassthrough(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "t"}, []string{"/swagger/"})
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	for _, p := range []string{"/swagger/", "/swagger/index.html", "/swagger/doc.json"} {
		called = false
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
		if !called {
			t.Errorf("path %s not passed through", p)
		}
	}
	called = false
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/events", nil))
	if called {
		t.Error("protected route passed through")
	}
}

func TestMiddleware_InjectsTenant(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "tenant-a"}, nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tid := tenant.MustFromContext(r.Context()); tid != "tenant-a" {
			t.Errorf("got %q", tid)
		}
	}))
	req := httptest.NewRequest("GET", "/v1/events", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestMiddleware_SpoofingIgnored(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "tenant-a"}, nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tid := tenant.MustFromContext(r.Context()); tid != "tenant-a" {
			t.Errorf("spoof leaked: %q", tid)
		}
	}))
	req := httptest.NewRequest("GET", "/v1/events", nil)
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("X-Tenant-ID", "tenant-evil")
	req.Header.Set("X-Tenant", "tenant-evil")
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestMiddleware_ConcurrentNoLeak(t *testing.T) {
	mw := auth.Middleware(auth.LegacyResolver{Key: "k", Tenant: "tenant-a"}, nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tid := tenant.MustFromContext(r.Context()); tid != "tenant-a" {
			t.Errorf("leak: %q", tid)
		}
	}))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/v1/events", nil)
			req.Header.Set("Authorization", "Bearer k")
			h.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
}
