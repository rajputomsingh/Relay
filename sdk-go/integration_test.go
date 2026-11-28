package relay_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	relay "github.com/rajputomsingh/relay/sdk-go"

	"github.com/rajputomsingh/relay/internal/auth"
	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/endpoints"
	"github.com/rajputomsingh/relay/internal/handlers"
	"github.com/rajputomsingh/relay/internal/lifecycle"
	"github.com/rajputomsingh/relay/internal/tenant"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

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
	defer func() {
		if err := client.Disconnect(ctx); err != nil {
			t.Errorf("disconnect mongo client: %v", err)
		}
	}()

	db := client.Database("relay_sdk_integration_test")
	defer func() {
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	}()

	events := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")

	aggregator := lifecycle.NewAggregator(events, deliveries)

	apiKey := "test-sdk-api-key-with-at-least-32-chars"
	testTenantID := tenant.TenantID("test-sdk-tenant")

	fanout := delivery.NewFanout(events, endpointsCol, deliveries, aggregator, nil)

	eventsHandler := handlers.NewEventsHandler(
		events, deliveries, apiKey, fanout, aggregator,
	)
	inspectionHandler := handlers.NewInspectionHandler(
		events, attempts, deliveries, apiKey,
	)
	endpointsHandler := handlers.NewEndpointsHandler(
		endpoints.NewStore(endpointsCol),
	)
	eventsListHandler := handlers.NewEventsListHandler(events, apiKey)

	mux := http.NewServeMux()
	eventsHandler.Register(mux)
	eventsListHandler.Register(mux)
	inspectionHandler.Register(mux)
	endpointsHandler.Register(mux)

	wrapped := auth.Middleware(
		auth.LegacyResolver{Key: apiKey, Tenant: testTenantID},
		nil,
	)(mux)

	srv := httptest.NewServer(wrapped)
	defer srv.Close()

	sdk := relay.NewClient(apiKey, relay.WithBaseURL(srv.URL))

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

	evt, err := sdk.InspectEvent(ctx, resp.EventID)
	if err != nil {
		t.Fatalf("InspectEvent: %v", err)
	}
	if evt.DeliveryStatus == "" || evt.DeliveryStatus == "managed" {
		t.Logf("event status after send: %q (acceptable)", evt.DeliveryStatus)
	}

	listResp, err := sdk.ListEvents(ctx, relay.ListEventsParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(listResp.Events) == 0 {
		t.Errorf("ListEvents returned no events; want at least 1")
	}
}
