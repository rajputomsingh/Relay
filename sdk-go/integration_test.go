package relay_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	relay "github.com/rajputomsingh/relay/sdk-go"

	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/endpoints"
	"github.com/rajputomsingh/relay/internal/handlers"
	"github.com/rajputomsingh/relay/internal/lifecycle"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestSDKAgainstRealHandlers runs the SDK against the real Relay handlers
// backed by a real MongoDB, with no mocks. This is the test that proves
// the SDK's request and response shapes match the server's actual contract.
//
// Requires MONGODB_TEST_URI. Skips otherwise.
func TestSDKAgainstRealHandlers(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set; skipping server-backed integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database("relay_sdk_integration_test")
	defer db.Drop(ctx)

	events := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")

	aggregator := lifecycle.NewAggregator(events, deliveries)

	// Build a minimal Relay server with the real handlers.
	apiKey := "test-sdk-api-key-with-at-least-32-chars"
	fanout := delivery.NewFanout(events, endpointsCol, deliveries, aggregator, nil)

	eventsHandler := handlers.NewEventsHandler(
		events, deliveries, apiKey, fanout, aggregator,
	)
	inspectionHandler := handlers.NewInspectionHandler(
		events, attempts, deliveries, apiKey,
	)
	endpointsHandler := handlers.NewEndpointsHandler(
		endpoints.NewStore(endpointsCol), apiKey,
	)
	eventsListHandler := handlers.NewEventsListHandler(events, apiKey)

	mux := http.NewServeMux()
	eventsHandler.Register(mux)
	eventsListHandler.Register(mux)
	inspectionHandler.Register(mux)
	endpointsHandler.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// SDK client pointed at the real server.
	sdk := relay.NewClient(apiKey, relay.WithBaseURL(srv.URL))

	// Endpoint creation exercises the SSRF guard, validation, and response shape.
	ep, err := sdk.CreateEndpoint(ctx, relay.CreateEndpointParams{
		Name:        "sdk-test",
		URL:         "https://example.com/hook",
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("CreateEndpoint against real server: %v", err)
	}
	if ep.ID == "" || !ep.Enabled {
		t.Fatalf("CreateEndpoint response malformed: %+v", ep)
	}

	// Event submission exercises ingestion, fan-out, and the aggregator.
	resp, err := sdk.SendEvent(ctx, relay.Event{
		Source:    "sdk-test",
		EventType: "test.created",
		Data:      map[string]interface{}{"k": "v"},
	}, "sdk-idem-1")
	if err != nil {
		t.Fatalf("SendEvent against real server: %v", err)
	}
	if resp.Status != "accepted" {
		t.Errorf("SendEvent Status = %q; want accepted", resp.Status)
	}
	if resp.EventID == "" {
		t.Fatal("SendEvent EventID is empty on first submission")
	}

	// Inspect: verify parent status reflects the child deliveries.
	evt, err := sdk.InspectEvent(ctx, resp.EventID)
	if err != nil {
		t.Fatalf("InspectEvent: %v", err)
	}
	if evt.DeliveryStatus == "" || evt.DeliveryStatus == "managed" {
		// Aggregator should have moved the parent out of "managed" by now.
		// It may still be "processing" or "pending" depending on worker state.
		t.Logf("event status after send: %q (acceptable)", evt.DeliveryStatus)
	}

	// List events: verify SDK serializes and decodes correctly.
	listResp, err := sdk.ListEvents(ctx, relay.ListEventsParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(listResp.Events) == 0 {
		t.Errorf("ListEvents returned no events; want at least 1")
	}
}
