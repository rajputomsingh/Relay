package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testAPIKey = "test-relay-api-key-that-is-at-least-32-chars"

func newEventTestServer() http.Handler {
	mux := http.NewServeMux()
	NewEventsHandler(nil, testAPIKey, nil).Register(mux)
	return mux
}

func TestCreateEventRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		apiKey         string
		idempotencyKey string
		body           string
		wantStatus     int
	}{
		{
			name:       "rejects non-POST method",
			method:     http.MethodGet,
			apiKey:     testAPIKey,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "rejects missing API key",
			method:     http.MethodPost,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "rejects incorrect API key",
			method:     http.MethodPost,
			apiKey:     "incorrect-key",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "rejects missing idempotency key",
			method:     http.MethodPost,
			apiKey:     testAPIKey,
			body:       `{"source":"test","event_type":"test.event","data":{}}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:           "rejects malformed JSON",
			method:         http.MethodPost,
			apiKey:         testAPIKey,
			idempotencyKey: "test-001",
			body:           `{"source":`,
			wantStatus:     http.StatusBadRequest,
		},
		{
			name:           "rejects unknown JSON fields",
			method:         http.MethodPost,
			apiKey:         testAPIKey,
			idempotencyKey: "test-002",
			body:           `{"source":"test","event_type":"test.event","data":{},"unexpected":true}`,
			wantStatus:     http.StatusBadRequest,
		},
		{
			name:           "rejects missing source",
			method:         http.MethodPost,
			apiKey:         testAPIKey,
			idempotencyKey: "test-003",
			body:           `{"event_type":"test.event","data":{}}`,
			wantStatus:     http.StatusBadRequest,
		},
		{
			name:           "rejects missing event type",
			method:         http.MethodPost,
			apiKey:         testAPIKey,
			idempotencyKey: "test-004",
			body:           `{"source":"test","data":{}}`,
			wantStatus:     http.StatusBadRequest,
		},
		{
			name:           "rejects non-object data",
			method:         http.MethodPost,
			apiKey:         testAPIKey,
			idempotencyKey: "test-005",
			body:           `{"source":"test","event_type":"test.event","data":[]}`,
			wantStatus:     http.StatusBadRequest,
		},
		{
			name:           "rejects oversized idempotency key",
			method:         http.MethodPost,
			apiKey:         testAPIKey,
			idempotencyKey: strings.Repeat("a", 129),
			body:           `{"source":"test","event_type":"test.event","data":{}}`,
			wantStatus:     http.StatusBadRequest,
		},
	}

	handler := newEventTestServer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				tt.method,
				"/v1/events",
				strings.NewReader(tt.body),
			)

			if tt.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+tt.apiKey)
			}

			if tt.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", tt.idempotencyKey)
			}

			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d; response: %s",
					tt.wantStatus,
					rec.Code,
					rec.Body.String(),
				)
			}

			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
				t.Errorf("expected JSON content type, got %q", got)
			}
		})
	}
}

func TestCreateEventRejectsOversizedBody(t *testing.T) {
	handler := newEventTestServer()

	body := `{"source":"test","event_type":"test.event","data":{}}` +
		strings.Repeat(" ", maxEventBody+1)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/events",
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Idempotency-Key", "oversized-test")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			rec.Code,
			rec.Body.String(),
		)
	}
}

func TestReplayEventRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		apiKey     string
		path       string
		enabled    bool
		wantStatus int
	}{
		{
			name:       "rejects non-POST method",
			method:     http.MethodGet,
			apiKey:     testAPIKey,
			path:       "/v1/events/507f1f77bcf86cd799439011/replay",
			enabled:    true,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "rejects missing API key",
			method:     http.MethodPost,
			path:       "/v1/events/507f1f77bcf86cd799439011/replay",
			enabled:    true,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "rejects incorrect API key",
			method:     http.MethodPost,
			apiKey:     "incorrect-key",
			path:       "/v1/events/507f1f77bcf86cd799439011/replay",
			enabled:    true,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "rejects replay when delivery is disabled",
			method:     http.MethodPost,
			apiKey:     testAPIKey,
			path:       "/v1/events/507f1f77bcf86cd799439011/replay",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "rejects invalid event ID",
			method:     http.MethodPost,
			apiKey:     testAPIKey,
			path:       "/v1/events/not-an-object-id/replay",
			enabled:    true,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			NewEventsHandler(nil, testAPIKey, nil).Register(mux)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+tt.apiKey)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d; response: %s",
					tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}
