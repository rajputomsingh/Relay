package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newEventListTestServer() http.Handler {
	mux := http.NewServeMux()
	NewEventsListHandler(nil, testAPIKey).Register(mux)
	return mux
}

func TestEventListRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		apiKey     string
		wantStatus int
	}{
		{
			name:       "missing API key",
			path:       "/v1/events",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "incorrect API key",
			path:       "/v1/events",
			apiKey:     "incorrect-key",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid delivery status",
			path:       "/v1/events?delivery_status=unknown",
			apiKey:     testAPIKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate delivery status",
			path:       "/v1/events?delivery_status=failed&delivery_status=pending",
			apiKey:     testAPIKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "limit too large",
			path:       "/v1/events?limit=101",
			apiKey:     testAPIKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "limit zero",
			path:       "/v1/events?limit=0",
			apiKey:     testAPIKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid limit",
			path:       "/v1/events?limit=abc",
			apiKey:     testAPIKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid cursor",
			path:       "/v1/events?cursor=invalid!",
			apiKey:     testAPIKey,
			wantStatus: http.StatusBadRequest,
		},
	}

	handler := newEventListTestServer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+tt.apiKey)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d: %s",
					tt.wantStatus,
					recorder.Code,
					recorder.Body.String(),
				)
			}

			if !strings.Contains(recorder.Header().Get("Content-Type"), "application/json") {
				t.Errorf("expected JSON content type, got %q",
					recorder.Header().Get("Content-Type"))
			}
		})
	}
}

func TestEventListRejectsUnsupportedMethod(t *testing.T) {
	handler := newEventListTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/events", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
