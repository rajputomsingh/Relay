package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// fakePinger lets tests control readiness without a live MongoDB.
type fakePinger struct {
	err error
}

func (f *fakePinger) Ping(
	ctx context.Context,
	rp *readpref.ReadPref,
) error {
	return f.err
}

func newHealthTestMux(client pinger) *http.ServeMux {
	mux := http.NewServeMux()
	NewHealthHandler(client).Register(mux)
	return mux
}

func performHealthRequest(
	mux http.Handler,
	method, path string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder
}

func decodeHealthBody(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) map[string]string {
	t.Helper()

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf(
			"decode health response: %v; body=%s",
			err,
			recorder.Body.String(),
		)
	}
	return body
}

// ---------------------------------------------------------------
// Liveness
// ---------------------------------------------------------------

func TestLivenessReturnsOK(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{})

	recorder := performHealthRequest(mux, http.MethodGet, "/healthz")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	body := decodeHealthBody(t, recorder)
	if body["service"] != "relay" {
		t.Errorf("service = %q, want relay", body["service"])
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
	if len(body) != 2 {
		t.Errorf(
			"response has %d fields; want exactly 2: %v",
			len(body),
			body,
		)
	}
}

func TestLivenessRejectsNonGET(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{})

	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
	} {
		t.Run(method, func(t *testing.T) {
			recorder := performHealthRequest(mux, method, "/healthz")
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}
		})
	}
}

func TestLivenessDoesNotDependOnMongo(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{
		err: errors.New("mongo unreachable"),
	})

	recorder := performHealthRequest(mux, http.MethodGet, "/healthz")
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want 200 (liveness must not consult Mongo)",
			recorder.Code,
		)
	}
}

// ---------------------------------------------------------------
// Readiness
// ---------------------------------------------------------------

func TestReadinessReturnsOKWhenMongoReachable(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{})

	recorder := performHealthRequest(mux, http.MethodGet, "/readyz")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	body := decodeHealthBody(t, recorder)
	if body["status"] != "ready" {
		t.Errorf("status = %q, want ready", body["status"])
	}
	if body["database"] != "connected" {
		t.Errorf("database = %q, want connected", body["database"])
	}
	if len(body) != 2 {
		t.Errorf(
			"response has %d fields; want exactly 2: %v",
			len(body),
			body,
		)
	}
}

func TestReadinessReturnsUnavailableWhenMongoUnreachable(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{
		err: errors.New("connection refused"),
	})

	recorder := performHealthRequest(mux, http.MethodGet, "/readyz")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}

	body := decodeHealthBody(t, recorder)
	if body["status"] != "not_ready" {
		t.Errorf("status = %q, want not_ready", body["status"])
	}
	if len(body) != 1 {
		t.Errorf(
			"response has %d fields; want exactly 1: %v",
			len(body),
			body,
		)
	}
}

func TestReadinessRejectsNonGET(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{})

	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
	} {
		t.Run(method, func(t *testing.T) {
			recorder := performHealthRequest(mux, method, "/readyz")
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}
		})
	}
}

// ---------------------------------------------------------------
// Response hygiene
// ---------------------------------------------------------------

func TestHealthResponsesDoNotLeakInternalDetails(t *testing.T) {
	mux := newHealthTestMux(&fakePinger{
		err: errors.New(
			"server selection error: " +
				"mongodb+srv://user:pass@cluster.example.net",
		),
	})

	recorder := performHealthRequest(mux, http.MethodGet, "/readyz")
	body := recorder.Body.String()

	for _, forbidden := range []string{
		"mongodb+srv://",
		"user:pass",
		"cluster.example.net",
		"server selection error",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf(
				"readiness response leaks %q: %s",
				forbidden,
				body,
			)
		}
	}
}
