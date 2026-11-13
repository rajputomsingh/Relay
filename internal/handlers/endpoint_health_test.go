package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeEndpointHealthStore struct {
	health endpointHealth
	err    error

	gotID  bson.ObjectID
	gotNow time.Time
}

func (f *fakeEndpointHealthStore) Health(
	_ context.Context,
	id bson.ObjectID,
	now time.Time,
) (endpointHealth, error) {
	f.gotID = id
	f.gotNow = now
	if f.err != nil {
		return endpointHealth{}, f.err
	}
	return f.health, nil
}

func newEndpointHealthTestMux(
	epStore endpointsStore,
	healthStore endpointHealthStore,
	now func() time.Time,
) *http.ServeMux {
	mux := http.NewServeMux()
	h := &EndpointHealthHandler{
		endpoints: epStore,
		health:    healthStore,
		apiKey:    testAPIKey,
		now:       now,
	}
	h.Register(mux)
	return mux
}

func TestEndpointHealthUnauthorized(t *testing.T) {
	mux := newEndpointHealthTestMux(&fakeEndpointsStore{}, &fakeEndpointHealthStore{}, nil)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/000000000000000000000000/health", "", "",
	)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Fatalf("unexpected error body: %v", body)
	}
}

func TestEndpointHealthInvalidID(t *testing.T) {
	mux := newEndpointHealthTestMux(&fakeEndpointsStore{}, &fakeEndpointHealthStore{}, nil)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/not-an-id/health", "", testAPIKey,
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointHealthNotFound(t *testing.T) {
	epStore := &fakeEndpointsStore{getItem: endpoints.Endpoint{ID: bson.NewObjectID()}}
	mux := newEndpointHealthTestMux(epStore, &fakeEndpointHealthStore{}, nil)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/000000000000000000000001/health", "", testAPIKey,
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointHealthEndpointStoreError(t *testing.T) {
	epStore := &fakeEndpointsStore{err: errors.New("boom")}
	mux := newEndpointHealthTestMux(epStore, &fakeEndpointHealthStore{}, nil)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/000000000000000000000000/health", "", testAPIKey,
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointHealthHealthStoreError(t *testing.T) {
	id := bson.NewObjectID()
	epStore := &fakeEndpointsStore{getItem: endpoints.Endpoint{ID: id}}
	healthStore := &fakeEndpointHealthStore{err: errors.New("boom")}
	mux := newEndpointHealthTestMux(epStore, healthStore, nil)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/"+id.Hex()+"/health", "", testAPIKey,
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointHealthNoHistory(t *testing.T) {
	id := bson.NewObjectID()
	epStore := &fakeEndpointsStore{getItem: endpoints.Endpoint{ID: id}}
	mux := newEndpointHealthTestMux(epStore, &fakeEndpointHealthStore{}, nil)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/"+id.Hex()+"/health", "", testAPIKey,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"last_success_at":null`) ||
		!strings.Contains(rec.Body.String(), `"last_failure_at":null`) {
		t.Fatalf("expected null timestamps, got %s", rec.Body.String())
	}
	for _, key := range []string{"p50_latency_ms", "p95_latency_ms", "p99_latency_ms"} {
		if !strings.Contains(rec.Body.String(), `"`+key+`":null`) {
			t.Fatalf("expected explicit null for %s, got %s", key, rec.Body.String())
		}
	}

	var resp endpointHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Attempts1h != 0 || resp.SuccessRate1h != 0 {
		t.Fatalf("expected zero attempts and rate, got %+v", resp)
	}
	if resp.LastSuccessAt != nil || resp.LastFailureAt != nil {
		t.Fatalf("expected nil timestamps, got %+v", resp)
	}
}

func TestEndpointHealthMixedOutcomes(t *testing.T) {
	id := bson.NewObjectID()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	lastSuccess := now.Add(-15 * time.Minute)
	lastFailure := now.Add(-5 * time.Minute)
	latencies := [3]float64{0, 187, 512}

	epStore := &fakeEndpointsStore{getItem: endpoints.Endpoint{ID: id}}
	healthStore := &fakeEndpointHealthStore{health: endpointHealth{
		Attempts1h:          8,
		Successes1h:         6,
		Failures1h:          2,
		ConsecutiveFailures: 3,
		LastSuccessAt:       &lastSuccess,
		LastFailureAt:       &lastFailure,
		P50LatencyMS:        &latencies[0],
		P95LatencyMS:        &latencies[1],
		P99LatencyMS:        &latencies[2],
	}}
	mux := newEndpointHealthTestMux(
		epStore, healthStore, func() time.Time { return now },
	)

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/"+id.Hex()+"/health", "", testAPIKey,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp endpointHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.SuccessRate1h != 75 {
		t.Fatalf("expected success rate 75, got %v", resp.SuccessRate1h)
	}
	if resp.Attempts1h != 8 || resp.Successes1h != 6 || resp.Failures1h != 2 {
		t.Fatalf("unexpected counts: %+v", resp)
	}
	if resp.ConsecutiveFailures != 3 {
		t.Fatalf("expected 3 consecutive failures, got %d", resp.ConsecutiveFailures)
	}
	if resp.LastSuccessAt == nil || !resp.LastSuccessAt.Equal(lastSuccess) {
		t.Fatalf("unexpected last_success_at: %v", resp.LastSuccessAt)
	}
	if resp.LastFailureAt == nil || !resp.LastFailureAt.Equal(lastFailure) {
		t.Fatalf("unexpected last_failure_at: %v", resp.LastFailureAt)
	}

	for i, got := range []*float64{resp.P50LatencyMS, resp.P95LatencyMS, resp.P99LatencyMS} {
		if got == nil || *got != latencies[i] {
			t.Fatalf("latency %d = %v, want %v", i, got, latencies[i])
		}
	}

	if healthStore.gotID != id {
		t.Fatalf("store received id %v, want %v", healthStore.gotID, id)
	}
	if !healthStore.gotNow.Equal(now) {
		t.Fatalf("store received now %v, want %v", healthStore.gotNow, now)
	}
}
