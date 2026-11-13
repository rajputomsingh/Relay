package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetEndpointHealth(t *testing.T) {
	lastSuccess := time.Date(2026, 11, 12, 10, 0, 0, 123000000, time.UTC)
	lastFailure := time.Date(2026, 11, 12, 10, 5, 0, 0, time.UTC)
	tests := []struct {
		name string
		body string
		want EndpointHealth
	}{
		{
			name: "mixed outcomes",
			body: `{"success_rate_1h":97.5,"attempts_1h":40,"successes_1h":39,"failures_1h":1,"consecutive_failures":2,"last_success_at":"2026-11-12T10:00:00.123Z","last_failure_at":"2026-11-12T10:05:00Z"}`,
			want: EndpointHealth{
				SuccessRate1h: 97.5, Attempts1h: 40, Successes1h: 39,
				Failures1h: 1, ConsecutiveFailures: 2,
				LastSuccessAt: &lastSuccess, LastFailureAt: &lastFailure,
			},
		},
		{
			name: "no history",
			body: `{"success_rate_1h":0,"attempts_1h":0,"successes_1h":0,"failures_1h":0,"consecutive_failures":0,"last_success_at":null,"last_failure_at":null}`,
			want: EndpointHealth{},
		},
		{
			name: "only successes",
			body: `{"success_rate_1h":100,"attempts_1h":1,"successes_1h":1,"failures_1h":0,"consecutive_failures":0,"last_success_at":"2026-11-12T10:00:00.123Z","last_failure_at":null}`,
			want: EndpointHealth{
				SuccessRate1h: 100, Attempts1h: 1, Successes1h: 1,
				LastSuccessAt: &lastSuccess,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.RequestURI != "/v1/endpoints/6af2f46f64e24ff56d2b3baa/health" {
					t.Errorf("request = %s %s; want GET endpoint health", r.Method, r.RequestURI)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
					t.Errorf("Authorization = %q; want Bearer test-key", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			client := NewClient("test-key", WithBaseURL(server.URL))
			got, err := client.GetEndpointHealth(context.Background(), "6af2f46f64e24ff56d2b3baa")
			if err != nil {
				t.Fatalf("GetEndpointHealth: %v", err)
			}
			if !reflect.DeepEqual(got, &tt.want) {
				t.Errorf("GetEndpointHealth = %+v; want %+v", got, &tt.want)
			}
		})
	}
}

func TestGetEndpointHealthAPIErrors(t *testing.T) {
	tests := []struct {
		status  int
		message string
		matches func(error) bool
	}{
		{http.StatusNotFound, "endpoint not found", IsNotFound},
		{http.StatusUnauthorized, "unauthorized", IsUnauthorized},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprintf(w, `{"error":%q}`, tt.message)
			}))
			defer server.Close()

			client := NewClient("test-key", WithBaseURL(server.URL))
			got, err := client.GetEndpointHealth(context.Background(), "6af2f46f64e24ff56d2b3baa")
			if got != nil {
				t.Errorf("response = %+v; want nil", got)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v; want wrapped *APIError", err)
			}
			if apiErr.StatusCode != tt.status || apiErr.Message != tt.message {
				t.Errorf("APIError = %+v; want status %d, message %q", apiErr, tt.status, tt.message)
			}
			if !tt.matches(err) {
				t.Errorf("status helper did not recognize %v", err)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d; want 1 (no retries)", got)
			}
		})
	}
}

func TestGetEndpointHealthEscapesID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/endpoints/ep%2Fone%3Fnext%23id/health"; r.RequestURI != want {
			t.Errorf("RequestURI = %q; want %q", r.RequestURI, want)
		}
		_, _ = fmt.Fprint(w, `{"last_success_at":null,"last_failure_at":null}`)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	if _, err := client.GetEndpointHealth(context.Background(), "ep/one?next#id"); err != nil {
		t.Fatalf("GetEndpointHealth: %v", err)
	}
}

func TestGetEndpointHealthRequiresID(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	got, err := client.GetEndpointHealth(context.Background(), "")
	if got != nil || err == nil || !strings.Contains(err.Error(), "endpointID is required") {
		t.Fatalf("GetEndpointHealth with empty ID = (%+v, %v); want validation error", got, err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests = %d; want 0", got)
	}
}

func TestGetEndpointHealthDecodeError(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON":    `{"attempts_1h":`,
		"invalid timestamp": `{"last_success_at":"not-a-timestamp"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, body)
			}))
			defer server.Close()

			client := NewClient("test-key", WithBaseURL(server.URL))
			got, err := client.GetEndpointHealth(context.Background(), "6af2f46f64e24ff56d2b3baa")
			if got != nil || err == nil || !strings.Contains(err.Error(), "decode response") {
				t.Fatalf("GetEndpointHealth = (%+v, %v); want decode error", got, err)
			}
		})
	}
}

func TestGetEndpointHealthCanceledContext(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	client.retryConfig = RetryConfig{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := client.GetEndpointHealth(ctx, "6af2f46f64e24ff56d2b3baa")
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("GetEndpointHealth = (%+v, %v); want context.Canceled", got, err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests = %d; want 0", got)
	}
}
