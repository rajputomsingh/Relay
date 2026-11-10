package reconciler

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// noopFanouter records the events it was asked to fan-out.
type noopFanouter struct {
	called []bson.ObjectID
}

func (n *noopFanouter) FanOutEvent(
	_ context.Context,
	eventID bson.ObjectID,
	_ string,
	_ string,
) error {
	n.called = append(n.called, eventID)
	return nil
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(
		os.Stderr,
		&slog.HandlerOptions{Level: slog.LevelError},
	))
}

// TestPartialFanoutDetectedAndRecovered seeds an event with
// fanout_status=incomplete and one child delivery, then runs one
// reconciler pass. The reconciler must call FanOutEvent for it.
func TestPartialFanoutDetectedAndRecovered(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}

	ctx := context.Background()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database("relay_reconciler_partial_fanout_test")
	defer db.Drop(ctx)

	eventsCol := db.Collection("events")
	deliveriesCol := db.Collection("deliveries")

	now := time.Now().UTC()
	oldEnough := now.Add(-10 * time.Minute)

	eventID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":                   eventID,
		"source":                "test",
		"event_type":            "test.created",
		"received_at":           oldEnough,
		"status":                "accepted",
		"delivery_status":       "processing",
		"fanout_status":         "incomplete",
		"fanout_expected_count": 3,
		"fanout_completed_at":   oldEnough,
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := deliveriesCol.InsertOne(ctx, bson.M{
		"_id":          bson.NewObjectID(),
		"event_id":     eventID,
		"endpoint_id":  bson.NewObjectID(),
		"status":       "pending",
		"attempts":     0,
		"max_attempts": 3,
		"created_at":   oldEnough,
		"updated_at":   oldEnough,
	}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	store := NewStore(db)
	fanouter := &noopFanouter{}
	cfg := Config{
		Interval:        60 * time.Second,
		OrphanThreshold: 5 * time.Minute,
	}
	rec := New(store, fanouter, nil, cfg, newSilentLogger())

	rec.reconcile(ctx)

	if len(fanouter.called) != 1 {
		t.Fatalf("FanOutEvent calls = %d; want 1", len(fanouter.called))
	}
	if fanouter.called[0] != eventID {
		t.Errorf("FanOutEvent called with %s; want %s",
			fanouter.called[0].Hex(), eventID.Hex())
	}
}

// TestCompletedZeroDestinationNotRefannedOut seeds an event with
// fanout_status=completed and fanout_expected_count=0 (a healthy
// no_destination event). The reconciler must NOT call FanOutEvent.
func TestCompletedZeroDestinationNotRefannedOut(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}

	ctx := context.Background()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database("relay_reconciler_no_destination_test")
	defer db.Drop(ctx)

	eventsCol := db.Collection("events")

	now := time.Now().UTC()
	oldEnough := now.Add(-10 * time.Minute)

	noDestID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":                   noDestID,
		"source":                "test",
		"event_type":            "test.created",
		"received_at":           oldEnough,
		"status":                "accepted",
		"delivery_status":       "no_destination",
		"fanout_status":         "completed",
		"fanout_expected_count": 0,
		"fanout_completed_at":   oldEnough,
	}); err != nil {
		t.Fatalf("insert no_destination event: %v", err)
	}

	store := NewStore(db)
	fanouter := &noopFanouter{}
	cfg := Config{
		Interval:        60 * time.Second,
		OrphanThreshold: 5 * time.Minute,
	}
	rec := New(store, fanouter, nil, cfg, newSilentLogger())

	rec.reconcile(ctx)

	if len(fanouter.called) != 0 {
		t.Fatalf("FanOutEvent called %d times; want 0 (no_destination is terminal)",
			len(fanouter.called))
	}

	var got bson.M
	if err := eventsCol.FindOne(ctx, bson.M{"_id": noDestID}).Decode(&got); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if got["delivery_status"] != "no_destination" {
		t.Errorf("status = %v; want no_destination (unchanged)", got["delivery_status"])
	}
}
