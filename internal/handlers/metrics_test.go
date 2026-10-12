package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeMetricsStore struct {
	snapshot metricsSnapshot
	err      error
	called   bool
}

func (s *fakeMetricsStore) Snapshot(
	_ context.Context,
) (metricsSnapshot, error) {
	s.called = true
	return s.snapshot, s.err
}

func newTestMetricsHandler(
	store metricsStore,
	apiKey string,
) *MetricsHandler {
	return &MetricsHandler{
		store:  store,
		apiKey: apiKey,
	}
}

func performMetricsRequest(
	handler *MetricsHandler,
	method string,
	apiKey string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Method = method

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	recorder := httptest.NewRecorder()
	handler.Register(http.NewServeMux())
	handler.serve(recorder, req)

	return recorder
}

func TestMetricsHandlerRejectsUnauthorizedRequests(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
	}{
		{
			name:   "missing token",
			apiKey: "",
		},
		{
			name:   "invalid token",
			apiKey: "wrong-key",
		},
	}

	store := &fakeMetricsStore{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestMetricsHandler(store, "correct-key")
			recorder := performMetricsRequest(
				handler,
				http.MethodGet,
				tt.apiKey,
			)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf(
					"status = %d, want %d",
					recorder.Code,
					http.StatusUnauthorized,
				)
			}

			if store.called {
				t.Fatal("store was called for an unauthorized request")
			}
		})
	}
}

func TestMetricsHandlerRejectsEmptyConfiguredAPIKey(t *testing.T) {
	store := &fakeMetricsStore{}
	handler := newTestMetricsHandler(store, "")

	recorder := performMetricsRequest(
		handler,
		http.MethodGet,
		"some-key",
	)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"status = %d, want %d",
			recorder.Code,
			http.StatusUnauthorized,
		)
	}

	if store.called {
		t.Fatal("store was called when no API key is configured")
	}
}

func TestMetricsHandlerRejectsNonGETRequests(t *testing.T) {
	store := &fakeMetricsStore{}
	handler := newTestMetricsHandler(store, "correct-key")

	recorder := performMetricsRequest(
		handler,
		http.MethodPost,
		"correct-key",
	)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf(
			"status = %d, want %d",
			recorder.Code,
			http.StatusMethodNotAllowed,
		)
	}

	if store.called {
		t.Fatal("store was called for a non-GET request")
	}
}

func TestMetricsHandlerWritesPrometheusMetrics(t *testing.T) {
	store := &fakeMetricsStore{
		snapshot: metricsSnapshot{
			EventCounts: map[string]int64{
				"pending":    3,
				"processing": 1,
				"delivered":  7,
				"failed":     2,
				"disabled":   0,
			},
			AttemptsTotal: 12,
			RetriesTotal:  4,
			DurationBucketCounts: map[int64]int64{
				100:   2,
				500:   5,
				1000:  7,
				5000:  9,
				10000: 11,
			},
			DurationCount:      12,
			DurationSumSeconds: 8.75,
		},
	}

	handler := newTestMetricsHandler(store, "correct-key")
	recorder := performMetricsRequest(
		handler,
		http.MethodGet,
		"correct-key",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d; body: %s",
			recorder.Code,
			http.StatusOK,
			recorder.Body.String(),
		)
	}

	if got := recorder.Header().Get("Content-Type"); got !=
		"text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	body := recorder.Body.String()

	expectedMetrics := []string{
		"# TYPE relay_events gauge",
		`relay_events{status="pending"} 3`,
		`relay_events{status="processing"} 1`,
		`relay_events{status="delivered"} 7`,
		`relay_events{status="failed"} 2`,
		`relay_events{status="disabled"} 0`,
		"# TYPE relay_delivery_attempts gauge",
		"relay_delivery_attempts 12",
		"# TYPE relay_delivery_retries gauge",
		"relay_delivery_retries 4",
		"# TYPE relay_delivery_duration_seconds histogram",
		`relay_delivery_duration_seconds_bucket{le="100"} 2`,
		`relay_delivery_duration_seconds_bucket{le="500"} 5`,
		`relay_delivery_duration_seconds_bucket{le="1000"} 7`,
		`relay_delivery_duration_seconds_bucket{le="5000"} 9`,
		`relay_delivery_duration_seconds_bucket{le="10000"} 11`,
		`relay_delivery_duration_seconds_bucket{le="+Inf"} 12`,
		"relay_delivery_duration_seconds_sum 8.75",
		"relay_delivery_duration_seconds_count 12",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics output missing %q", expected)
		}
	}

	if !store.called {
		t.Fatal("store was not called for an authorized request")
	}
}

func TestMetricsHandlerReturnsUnavailableOnStoreError(t *testing.T) {
	store := &fakeMetricsStore{
		err: errors.New("sensitive database connection details"),
	}

	handler := newTestMetricsHandler(store, "correct-key")
	recorder := performMetricsRequest(
		handler,
		http.MethodGet,
		"correct-key",
	)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"status = %d, want %d",
			recorder.Code,
			http.StatusServiceUnavailable,
		)
	}

	if !strings.Contains(
		recorder.Body.String(),
		"metrics temporarily unavailable",
	) {
		t.Errorf("response does not contain the expected generic error")
	}

	if strings.Contains(
		recorder.Body.String(),
		"sensitive database connection details",
	) {
		t.Error("response leaked internal database error details")
	}
}

func TestWriteMetricsHandlesEmptySnapshot(t *testing.T) {
	snapshot := metricsSnapshot{
		EventCounts:          map[string]int64{},
		DurationBucketCounts: map[int64]int64{},
	}

	recorder := httptest.NewRecorder()
	writeMetrics(recorder, snapshot)

	body := recorder.Body.String()

	expectedMetrics := []string{
		`relay_events{status="pending"} 0`,
		`relay_events{status="delivered"} 0`,
		"relay_delivery_attempts 0",
		"relay_delivery_retries 0",
		`relay_delivery_duration_seconds_bucket{le="+Inf"} 0`,
		"relay_delivery_duration_seconds_sum 0",
		"relay_delivery_duration_seconds_count 0",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("empty snapshot output missing %q", expected)
		}
	}
}
