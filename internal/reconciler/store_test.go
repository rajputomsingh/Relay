package reconciler

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestFindOrphanedEvents(t *testing.T) {
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

	db := client.Database("relay_reconciler_test")
	defer db.Drop(ctx)

	// Seed an orphaned event (older than 5 mins)
	eventsCol := db.Collection("events")
	oldTime := time.Now().UTC().Add(-10 * time.Minute)

	eventID := bson.NewObjectID()
	_, err = eventsCol.InsertOne(ctx, bson.M{
		"_id":        eventID,
		"source":     "test",
		"event_type": "user.created",
		"created_at": oldTime,
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Seed a normal event with a delivery (should not be returned)
	normalEventID := bson.NewObjectID()
	eventsCol.InsertOne(ctx, bson.M{
		"_id":        normalEventID,
		"source":     "test",
		"event_type": "user.created",
		"created_at": oldTime,
	})
	db.Collection("deliveries").InsertOne(ctx, bson.M{
		"event_id": normalEventID,
		"status":   "pending",
	})

	// Run the reconciler query
	store := NewStore(db)
	threshold := time.Now().UTC().Add(-5 * time.Minute)

	orphaned, err := store.FindOrphanedEvents(ctx, threshold)
	if err != nil {
		t.Fatalf("find orphaned: %v", err)
	}

	// Verify
	if len(orphaned) != 1 {
		t.Fatalf("expected 1 orphaned event, got %d", len(orphaned))
	}
	if orphaned[0].ID != eventID {
		t.Errorf("expected ID %s, got %s", eventID.Hex(), orphaned[0].ID.Hex())
	}
}
