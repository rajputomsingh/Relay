package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendEvent(t *testing.T) {
	// 1. Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header")
		}
		if r.Header.Get("Idempotency-Key") != "idem-123" {
			t.Errorf("missing idempotency key")
		}

		// Return mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"evt_123","status":"pending"}`))
	}))
	defer server.Close()

	// 2. Initialize client pointing at the mock server
	client := NewClient("test-key", WithBaseURL(server.URL))

	// 3. Send an event
	resp, err := client.SendEvent(context.Background(), Event{
		Source:    "test",
		EventType: "user.created",
		Data:      map[string]interface{}{"user_id": 1},
	}, "idem-123")

	// 4. Assertions
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.ID != "evt_123" {
		t.Errorf("expected evt_123, got %s", resp.ID)
	}
}
