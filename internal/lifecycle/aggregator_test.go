package lifecycle

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func newAggregatorTestDB(t *testing.T, dbName string) *mongo.Database {
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

func seedEvent(
	t *testing.T,
	events *mongo.Collection,
	expected int,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	doc := bson.M{
		"_id":                   id,
		"source":                "test",
		"event_type":            "test.created",
		"received_at":           time.Now().UTC(),
		"status":                "accepted",
		"delivery_status":       "managed",
		"fanout_status":         "completed",
		"fanout_expected_count": expected,
		"fanout_completed_at":   time.Now().UTC(),
	}
	if _, err := events.InsertOne(context.Background(), doc); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

func seedDelivery(
	t *testing.T,
	deliveries *mongo.Collection,
	eventID bson.ObjectID,
	status string,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	doc := bson.M{
		"_id":          id,
		"event_id":     eventID,
		"endpoint_id":  bson.NewObjectID(),
		"status":       status,
		"attempts":     0,
		"max_attempts": 3,
		"created_at":   time.Now().UTC(),
		"updated_at":   time.Now().UTC(),
	}
	if _, err := deliveries.InsertOne(context.Background(), doc); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}
	return id
}

func readEventStatus(
	t *testing.T,
	events *mongo.Collection,
	id bson.ObjectID,
) string {
	t.Helper()

	var got bson.M
	if err := events.FindOne(
		context.Background(), bson.M{"_id": id},
	).Decode(&got); err != nil {
		t.Fatalf("read event: %v", err)
	}
	status, _ := got["delivery_status"].(string)
	return status
}

func TestAggregateNoDestination(t *testing.T) {
	db := newAggregatorTestDB(t, "relay_lifecycle_no_destination")
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	eventID := seedEvent(t, events, 0)

	agg := NewAggregator(events, deliveries)
	if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
		t.Fatalf("AggregateEvent: %v", err)
	}

	if got := readEventStatus(t, events, eventID); got != StatusNoDestination {
		t.Errorf("status = %q, want %q", got, StatusNoDestination)
	}
}

func TestAggregateAllDelivered(t *testing.T) {
	db := newAggregatorTestDB(t, "relay_lifecycle_delivered")
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	eventID := seedEvent(t, events, 2)
	seedDelivery(t, deliveries, eventID, "delivered")
	seedDelivery(t, deliveries, eventID, "delivered")

	agg := NewAggregator(events, deliveries)
	if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
		t.Fatalf("AggregateEvent: %v", err)
	}

	if got := readEventStatus(t, events, eventID); got != StatusDelivered {
		t.Errorf("status = %q, want %q", got, StatusDelivered)
	}
}

func TestAggregateAllFailed(t *testing.T) {
	db := newAggregatorTestDB(t, "relay_lifecycle_failed")
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	eventID := seedEvent(t, events, 2)
	seedDelivery(t, deliveries, eventID, "failed")
	seedDelivery(t, deliveries, eventID, "failed")

	agg := NewAggregator(events, deliveries)
	if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
		t.Fatalf("AggregateEvent: %v", err)
	}

	if got := readEventStatus(t, events, eventID); got != StatusFailed {
		t.Errorf("status = %q, want %q", got, StatusFailed)
	}
}

func TestAggregateMixedOutcomeIsFailed(t *testing.T) {
	db := newAggregatorTestDB(t, "relay_lifecycle_mixed")
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	eventID := seedEvent(t, events, 3)
	seedDelivery(t, deliveries, eventID, "delivered")
	seedDelivery(t, deliveries, eventID, "delivered")
	seedDelivery(t, deliveries, eventID, "failed")

	agg := NewAggregator(events, deliveries)
	if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
		t.Fatalf("AggregateEvent: %v", err)
	}

	if got := readEventStatus(t, events, eventID); got != StatusFailed {
		t.Errorf("status = %q, want %q", got, StatusFailed)
	}
}

func TestAggregatePendingAndProcessing(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{
			name:     "all pending",
			statuses: []string{"pending", "pending"},
			want:     StatusPending,
		},
		{
			name:     "one processing",
			statuses: []string{"delivered", "processing"},
			want:     StatusProcessing,
		},
		{
			name:     "pending mixed with processing",
			statuses: []string{"pending", "processing"},
			want:     StatusProcessing,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// MongoDB forbids spaces in database names, so use an
			// index-based name rather than tc.name.
			db := newAggregatorTestDB(
				t,
				fmt.Sprintf("relay_lifecycle_inflight_%d", i),
			)
			events := db.Collection("events")
			deliveries := db.Collection("deliveries")

			eventID := seedEvent(t, events, len(tc.statuses))
			for _, s := range tc.statuses {
				seedDelivery(t, deliveries, eventID, s)
			}

			agg := NewAggregator(events, deliveries)
			if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
				t.Fatalf("AggregateEvent: %v", err)
			}
			if got := readEventStatus(t, events, eventID); got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAggregateIdempotent(t *testing.T) {
	db := newAggregatorTestDB(t, "relay_lifecycle_idempotent")
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	eventID := seedEvent(t, events, 2)
	seedDelivery(t, deliveries, eventID, "delivered")
	seedDelivery(t, deliveries, eventID, "delivered")

	agg := NewAggregator(events, deliveries)

	for i := 0; i < 3; i++ {
		if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}

	if got := readEventStatus(t, events, eventID); got != StatusDelivered {
		t.Errorf("status = %q, want %q", got, StatusDelivered)
	}
}

func TestAggregateRespectsDisabled(t *testing.T) {
	db := newAggregatorTestDB(t, "relay_lifecycle_disabled")
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	eventID := bson.NewObjectID()
	if _, err := events.InsertOne(context.Background(), bson.M{
		"_id":             eventID,
		"source":          "test",
		"event_type":      "test.created",
		"received_at":     time.Now().UTC(),
		"status":          "accepted",
		"delivery_status": "disabled",
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	agg := NewAggregator(events, deliveries)
	if err := agg.AggregateEvent(context.Background(), eventID); err != nil {
		t.Fatalf("AggregateEvent: %v", err)
	}

	if got := readEventStatus(t, events, eventID); got != StatusDisabled {
		t.Errorf("status = %q, want %q", got, StatusDisabled)
	}
}
