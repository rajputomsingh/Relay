package delivery

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestDeliveryUniqueKeyShape verifies that the delivery document carries the
// (event_id, endpoint_id) pair that the unique index relies on.
func TestDeliveryUniqueKeyShape(t *testing.T) {
	eventID := bson.NewObjectID()
	endpointID := bson.NewObjectID()

	d := Delivery{
		EventID:     eventID,
		EndpointID:  endpointID,
		Status:      StatusPending,
		MaxAttempts: 8,
	}

	if d.EventID != eventID {
		t.Fatalf("event id mismatch")
	}
	if d.EndpointID != endpointID {
		t.Fatalf("endpoint id mismatch")
	}
	if d.IsTerminal() {
		t.Fatal("pending delivery should not be terminal")
	}

	d.Status = StatusDelivered
	if !d.IsTerminal() {
		t.Fatal("delivered delivery should be terminal")
	}

	d.Status = StatusFailed
	if !d.IsTerminal() {
		t.Fatal("failed delivery should be terminal")
	}
}

// TestEventFilterRespectedByFanoutLogic mirrors the filter decision
// that Fanout.CreateDeliveries applies while iterating endpoints.
func TestEventFilterRespectedByFanoutLogic(t *testing.T) {
	cases := []struct {
		name      string
		filter    endpoints.EventFilter
		source    string
		eventType string
		want      bool
	}{
		{
			name:      "no filter matches all",
			filter:    endpoints.EventFilter{},
			source:    "billing",
			eventType: "invoice.paid",
			want:      true,
		},
		{
			name: "source filter excludes",
			filter: endpoints.EventFilter{
				Sources: []string{"orders"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      false,
		},
		{
			name: "type filter excludes",
			filter: endpoints.EventFilter{
				EventTypes: []string{"order.created"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.filter.Matches(tc.source, tc.eventType)
			if got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

// newFanoutTestDB connects to the test MongoDB and returns a fresh database
// scoped to the caller. Skipped when MONGODB_TEST_URI is unset.
func newFanoutTestDB(t *testing.T, dbName string) *mongo.Database {
	t.Helper()

	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		t.Fatalf("ping mongo: %v", err)
	}

	db := client.Database(dbName)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer cleanupCancel()
		_ = db.Drop(cleanupCtx)
		_ = client.Disconnect(cleanupCtx)
	})

	return db
}

func seedFanoutEvent(
	t *testing.T,
	events *mongo.Collection,
	eventID bson.ObjectID,
	source, eventType string,
) {
	t.Helper()

	if _, err := events.InsertOne(context.Background(), bson.M{
		"_id":             eventID,
		"source":          source,
		"event_type":      eventType,
		"data":            bson.M{},
		"idempotency_key": "fanout-test-" + eventID.Hex(),
		"received_at":     time.Now().UTC(),
		"status":          "accepted",
		"delivery_status": "managed",
		"fanout_status":   FanoutStatusInProgress,
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func seedFanoutEndpoint(
	t *testing.T,
	endpoints *mongo.Collection,
	source, eventType string,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	doc := bson.M{
		"_id":          id,
		"name":         "ep-" + id.Hex(),
		"url":          "https://example.invalid/hook",
		"enabled":      true,
		"max_attempts": 3,
		"created_at":   time.Now().UTC(),
		"updated_at":   time.Now().UTC(),
	}
	if source != "" || eventType != "" {
		filter := bson.M{}
		if source != "" {
			filter["sources"] = []string{source}
		}
		if eventType != "" {
			filter["event_types"] = []string{eventType}
		}
		doc["event_filter"] = filter
	}
	if _, err := endpoints.InsertOne(context.Background(), doc); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	return id
}

func TestFanoutZeroMatchingEndpointsRecordsNoDestination(t *testing.T) {
	db := newFanoutTestDB(t, "relay_fanout_no_destination")
	ctx := context.Background()

	events := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")

	eventID := bson.NewObjectID()
	seedFanoutEvent(t, events, eventID, "billing", "invoice.paid")
	// No endpoints seeded — zero matches expected.

	f := NewFanout(events, endpointsCol, deliveries, nil)
	if err := f.CreateDeliveries(ctx, eventID, "billing", "invoice.paid"); err != nil {
		t.Fatalf("CreateDeliveries returned error for zero-endpoint case: %v", err)
	}

	var got bson.M
	if err := events.FindOne(ctx, bson.M{"_id": eventID}).Decode(&got); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if got["fanout_status"] != FanoutStatusCompleted {
		t.Errorf("fanout_status = %v, want %s", got["fanout_status"], FanoutStatusCompleted)
	}
	if got["fanout_expected_count"] != int32(0) {
		t.Errorf("fanout_expected_count = %v, want 0", got["fanout_expected_count"])
	}
	if _, present := got["fanout_completed_at"]; !present {
		t.Error("fanout_completed_at should be set")
	}

	// No deliveries created.
	count, err := deliveries.CountDocuments(ctx, bson.M{"event_id": eventID})
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 0 {
		t.Errorf("deliveries = %d, want 0", count)
	}
}

func TestFanoutMatchingEndpointsRecordsExpectedCount(t *testing.T) {
	db := newFanoutTestDB(t, "relay_fanout_match")
	ctx := context.Background()

	events := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")

	// Two matching, one non-matching.
	seedFanoutEndpoint(t, endpointsCol, "", "")              // matches all
	seedFanoutEndpoint(t, endpointsCol, "billing", "")       // matches source only
	seedFanoutEndpoint(t, endpointsCol, "", "order.created") // doesn't match

	eventID := bson.NewObjectID()
	seedFanoutEvent(t, events, eventID, "billing", "invoice.paid")

	f := NewFanout(events, endpointsCol, deliveries, nil)
	if err := f.CreateDeliveries(ctx, eventID, "billing", "invoice.paid"); err != nil {
		t.Fatalf("CreateDeliveries: %v", err)
	}

	var got bson.M
	if err := events.FindOne(ctx, bson.M{"_id": eventID}).Decode(&got); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if got["fanout_status"] != FanoutStatusCompleted {
		t.Errorf("fanout_status = %v, want %s", got["fanout_status"], FanoutStatusCompleted)
	}
	if got["fanout_expected_count"] != int32(2) {
		t.Errorf("fanout_expected_count = %v, want 2", got["fanout_expected_count"])
	}

	count, err := deliveries.CountDocuments(ctx, bson.M{"event_id": eventID})
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 2 {
		t.Errorf("deliveries = %d, want 2", count)
	}
}

func TestFanoutIdempotentOnRepeatedCall(t *testing.T) {
	db := newFanoutTestDB(t, "relay_fanout_idempotent")
	ctx := context.Background()

	events := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")

	seedFanoutEndpoint(t, endpointsCol, "", "")

	eventID := bson.NewObjectID()
	seedFanoutEvent(t, events, eventID, "billing", "invoice.paid")

	f := NewFanout(events, endpointsCol, deliveries, nil)
	if err := f.CreateDeliveries(ctx, eventID, "billing", "invoice.paid"); err != nil {
		t.Fatalf("first CreateDeliveries: %v", err)
	}
	if err := f.CreateDeliveries(ctx, eventID, "billing", "invoice.paid"); err != nil {
		t.Fatalf("second CreateDeliveries: %v", err)
	}

	count, err := deliveries.CountDocuments(ctx, bson.M{"event_id": eventID})
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 1 {
		t.Errorf("deliveries = %d, want 1 (idempotent)", count)
	}
}

func TestFanoutRerecordsStatusOnRecovery(t *testing.T) {
	db := newFanoutTestDB(t, "relay_fanout_recovery_status")
	ctx := context.Background()

	events := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")

	seedFanoutEndpoint(t, endpointsCol, "", "")

	eventID := bson.NewObjectID()
	seedFanoutEvent(t, events, eventID, "billing", "invoice.paid")

	// Simulate a prior incomplete fan-out.
	if _, err := events.UpdateOne(
		ctx,
		bson.M{"_id": eventID},
		bson.M{"$set": bson.M{
			"fanout_status":         FanoutStatusIncomplete,
			"fanout_expected_count": 1,
		}},
	); err != nil {
		t.Fatalf("seed incomplete state: %v", err)
	}

	f := NewFanout(events, endpointsCol, deliveries, nil)
	if err := f.CreateDeliveries(ctx, eventID, "billing", "invoice.paid"); err != nil {
		t.Fatalf("CreateDeliveries: %v", err)
	}

	var got bson.M
	if err := events.FindOne(ctx, bson.M{"_id": eventID}).Decode(&got); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if got["fanout_status"] != FanoutStatusCompleted {
		t.Errorf("fanout_status = %v, want %s (recovery must overwrite)",
			got["fanout_status"], FanoutStatusCompleted)
	}
}
