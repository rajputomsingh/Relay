package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockServer builds a test server that responds based on method+path,
// matching the real Relay handler behavior for the endpoints exercised
// by these tests.
func mockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/events":
			if r.Header.Get("Idempotency-Key") == "" {
				http.Error(w, `{"error":"missing idempotency key"}`, http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":          "accepted",
				"event_id":        "6af2f46f64e24ff56d2b3bab",
				"idempotency_key": r.Header.Get("Idempotency-Key"),
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/events/") &&
			strings.HasSuffix(r.URL.Path, "/deliveries"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"event_id": "6af2f46f64e24ff56d2b3bab",
				"deliveries": []any{
					map[string]any{
						"endpoint_id":  "6af2f46f64e24ff56d2b3baa",
						"status":       "delivered",
						"attempts":     1,
						"max_attempts": 3,
					},
				},
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/events/") &&
			strings.HasSuffix(r.URL.Path, "/attempts"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"event_id": "6af2f46f64e24ff56d2b3bab",
				"attempts": []any{
					map[string]any{
						"id":             "att_1",
						"event_id":       "6af2f46f64e24ff56d2b3bab",
						"endpoint_id":    "6af2f46f64e24ff56d2b3baa",
						"attempt_number": 1,
						"outcome":        "delivered",
						"http_status":    204,
					},
				},
				"limit":    50,
				"has_more": false,
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/events/"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                strings.TrimPrefix(r.URL.Path, "/v1/events/"),
				"source":            "test",
				"event_type":        "test.created",
				"delivery_status":   "delivered",
				"delivery_attempts": 1,
			})

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/replay"):
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":              "replay_queued",
				"event_id":            "6af2f46f64e24ff56d2b3bab",
				"idempotency_key":     "idem-123",
				"deliveries_requeued": 1,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/events":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"events":   []any{},
				"limit":    50,
				"has_more": false,
			})

		case r.Method == http.MethodPost && r.URL.Path == "/v1/endpoints":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                 "6af2f46f64e24ff56d2b3baa",
				"name":               "test",
				"url":                "https://example.com/hook",
				"has_signing_secret": false,
				"enabled":            true,
				"max_attempts":       3,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/endpoints":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"endpoints": []any{},
				"count":     0,
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/endpoints/"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           strings.TrimPrefix(r.URL.Path, "/v1/endpoints/"),
				"name":         "test",
				"url":          "https://example.com/hook",
				"enabled":      true,
				"max_attempts": 3,
			})

		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/endpoints/"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           strings.TrimPrefix(r.URL.Path, "/v1/endpoints/"),
				"name":         "test-updated",
				"url":          "https://example.com/hook",
				"enabled":      true,
				"max_attempts": 3,
			})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/endpoints/"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "disabled",
				"id":     strings.TrimPrefix(r.URL.Path, "/v1/endpoints/"),
			})

		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSendEvent(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	resp, err := client.SendEvent(context.Background(), Event{
		Source:    "test",
		EventType: "user.created",
		Data:      map[string]interface{}{"user_id": 1},
	}, "idem-123")
	if err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	if resp.Status != "accepted" {
		t.Errorf("Status = %q; want accepted", resp.Status)
	}
	if resp.EventID == "" {
		t.Error("EventID should not be empty on first submission")
	}
	if resp.IdempotencyKey != "idem-123" {
		t.Errorf("IdempotencyKey = %q; want idem-123", resp.IdempotencyKey)
	}
}

func TestSendEventRequiresIdempotencyKey(t *testing.T) {
	client := NewClient("test-key")
	_, err := client.SendEvent(context.Background(), Event{
		Source:    "test",
		EventType: "test.created",
		Data:      map[string]interface{}{},
	}, "")
	if err == nil {
		t.Fatal("SendEvent with empty idempotencyKey should return error")
	}
}

func TestInspectEvent(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	evt, err := client.InspectEvent(context.Background(), "6af2f46f64e24ff56d2b3bab")
	if err != nil {
		t.Fatalf("InspectEvent: %v", err)
	}
	if evt.DeliveryStatus != "delivered" {
		t.Errorf("DeliveryStatus = %q; want delivered", evt.DeliveryStatus)
	}
}

func TestListEvents(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	resp, err := client.ListEvents(context.Background(), ListEventsParams{
		DeliveryStatus: "failed",
		Limit:          20,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if resp.Limit != 50 {
		t.Errorf("Limit = %d; want 50 (mock server default)", resp.Limit)
	}
}

func TestReplayEvent(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	resp, err := client.ReplayEvent(context.Background(), "6af2f46f64e24ff56d2b3bab")
	if err != nil {
		t.Fatalf("ReplayEvent: %v", err)
	}
	if resp.DeliveriesRequeued != 1 {
		t.Errorf("DeliveriesRequeued = %d; want 1", resp.DeliveriesRequeued)
	}
}

func TestCreateEndpoint(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	ep, err := client.CreateEndpoint(context.Background(), CreateEndpointParams{
		Name:        "test",
		URL:         "https://example.com/hook",
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	if !ep.Enabled {
		t.Errorf("Enabled = false; want true")
	}
}

func TestListEndpoints(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	resp, err := client.ListEndpoints(context.Background())
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("Count = %d; want 0", resp.Count)
	}
}

func TestUpdateEndpoint(t *testing.T) {
	server := mockServer(t)
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	newName := "test-updated"
	ep, err := client.UpdateEndpoint(
		context.Background(),
		"6af2f46f64e24ff56d2b3baa",
		UpdateEndpointParams{Name: &newName},
	)
	if err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	if ep.Name != "test-updated" {
		t.Errorf("Name = %q; want test-updated", ep.Name)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	body := []byte(`{"event_id":"abc"}`)

	tsHeader, sigHeader, err := SignWebhookBody(secret, body)
	if err != nil {
		t.Fatalf("SignWebhookBody: %v", err)
	}

	// Valid signature, fresh timestamp: passes.
	if err := VerifyWebhookSignature(
		secret, tsHeader, sigHeader, body, 5*time.Minute,
	); err != nil {
		t.Fatalf("VerifyWebhookSignature rejected valid signature: %v", err)
	}

	// Tampered body: fails.
	if err := VerifyWebhookSignature(
		secret, tsHeader, sigHeader,
		[]byte(`{"event_id":"xyz"}`),
		5*time.Minute,
	); err == nil {
		t.Fatal("VerifyWebhookSignature accepted tampered body")
	}

	// Wrong secret: fails.
	if err := VerifyWebhookSignature(
		"ffffffffffffffffffffffffffffffff",
		tsHeader, sigHeader, body, 5*time.Minute,
	); err == nil {
		t.Fatal("VerifyWebhookSignature accepted wrong secret")
	}
}

func TestIsNotFound(t *testing.T) {
	// Construct an APIError with 404 and verify IsNotFound reports true.
	err := &APIError{StatusCode: 404, Message: "not found"}
	if !IsNotFound(err) {
		t.Error("IsNotFound should be true for 404")
	}
	if IsNotFound(&APIError{StatusCode: 500}) {
		t.Error("IsNotFound should be false for 500")
	}
}
