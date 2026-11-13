package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// ---------------------------------------------------------------
// Service health (/v1/health)
// ---------------------------------------------------------------

// fakeWorkerPoller lets tests control when the worker last polled.
type fakeWorkerPoller struct {
	lastPolledAt time.Time
}

func (f *fakeWorkerPoller) LastPolledAt() time.Time {
	return f.lastPolledAt
}

const testPollInterval = 2 * time.Second

func newServiceHealthTestMux(client pinger, worker workerPoller) *http.ServeMux {
	mux := http.NewServeMux()
	handler := NewHealthHandler(client)
	if worker != nil {
		handler.WithWorker(worker, testPollInterval)
	}
	handler.Register(mux)
	return mux
}

func decodeServiceHealthBody(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf(
			"decode service health response: %v; body=%s",
			err,
			recorder.Body.String(),
		)
	}
	return body
}

func TestServiceHealthReturnsOKWhenMongoAndWorkerHealthy(t *testing.T) {
	mux := newServiceHealthTestMux(
		&fakePinger{},
		&fakeWorkerPoller{lastPolledAt: time.Now()},
	)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	body := decodeServiceHealthBody(t, recorder)
	for field, want := range map[string]string{
		"status":  "ok",
		"mongo":   "ok",
		"worker":  "ok",
		"version": "dev",
	} {
		if body[field] != want {
			t.Errorf("%s = %v, want %q", field, body[field], want)
		}
	}
	if _, ok := body["uptime_seconds"].(float64); !ok {
		t.Errorf("uptime_seconds = %v, want a number", body["uptime_seconds"])
	}
}

// An idle worker polls an empty queue and processes nothing. It must
// still report ok, because liveness comes from the last poll, not the
// last delivery.
func TestServiceHealthIdleWorkerIsOK(t *testing.T) {
	mux := newServiceHealthTestMux(
		&fakePinger{},
		&fakeWorkerPoller{lastPolledAt: time.Now().Add(-testPollInterval)},
	)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := decodeServiceHealthBody(t, recorder)
	if body["worker"] != "ok" {
		t.Errorf("worker = %v, want ok", body["worker"])
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

func TestServiceHealthReportsStaleWorkerAsDegraded(t *testing.T) {
	mux := newServiceHealthTestMux(
		&fakePinger{},
		&fakeWorkerPoller{lastPolledAt: time.Now().Add(-4 * testPollInterval)},
	)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := decodeServiceHealthBody(t, recorder)
	if body["worker"] != "stale" {
		t.Errorf("worker = %v, want stale", body["worker"])
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
}

func TestServiceHealthReportsNeverStartedWorkerAsDisabled(t *testing.T) {
	mux := newServiceHealthTestMux(&fakePinger{}, &fakeWorkerPoller{})

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := decodeServiceHealthBody(t, recorder)
	if body["worker"] != "disabled" {
		t.Errorf("worker = %v, want disabled", body["worker"])
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
}

func TestServiceHealthWithoutWorkerReportsDisabled(t *testing.T) {
	mux := newServiceHealthTestMux(&fakePinger{}, nil)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	body := decodeServiceHealthBody(t, recorder)
	if body["worker"] != "disabled" {
		t.Errorf("worker = %v, want disabled", body["worker"])
	}
}

func TestServiceHealthReturnsUnavailableWhenMongoUnreachable(t *testing.T) {
	mux := newServiceHealthTestMux(
		&fakePinger{err: errors.New("connection refused")},
		&fakeWorkerPoller{lastPolledAt: time.Now()},
	)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}

	body := decodeServiceHealthBody(t, recorder)
	if body["status"] != "down" {
		t.Errorf("status = %v, want down", body["status"])
	}
	if body["mongo"] != "unreachable" {
		t.Errorf("mongo = %v, want unreachable", body["mongo"])
	}
	if _, ok := body["worker"]; ok {
		t.Errorf("worker = %v, want it omitted when MongoDB is down", body["worker"])
	}
}

func TestServiceHealthReportsConfiguredVersion(t *testing.T) {
	mux := http.NewServeMux()
	NewHealthHandler(&fakePinger{}).WithVersion("1.2.3").Register(mux)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	body := decodeServiceHealthBody(t, recorder)
	if body["version"] != "1.2.3" {
		t.Errorf("version = %v, want 1.2.3", body["version"])
	}
}

func TestServiceHealthRejectsNonGET(t *testing.T) {
	mux := newServiceHealthTestMux(&fakePinger{}, nil)

	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
	} {
		t.Run(method, func(t *testing.T) {
			recorder := performHealthRequest(mux, method, "/v1/health")
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}
		})
	}
}

func TestServiceHealthDoesNotLeakInternalDetails(t *testing.T) {
	mux := newServiceHealthTestMux(&fakePinger{
		err: errors.New(
			"server selection error: " +
				"mongodb+srv://user:pass@cluster.example.net",
		),
	}, nil)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	body := recorder.Body.String()

	for _, forbidden := range []string{
		"mongodb+srv://",
		"user:pass",
		"cluster.example.net",
		"server selection error",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("service health response leaks %q: %s", forbidden, body)
		}
	}
}
