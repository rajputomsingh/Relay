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

// newReconcilerTestDB connects to the test MongoDB and returns a fresh
// database scoped to the caller. Skipped when MONGODB_TEST_URI is unset.
func newReconcilerTestDB(t *testing.T, dbName string) *mongo.Database {
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

func TestFindOrphanedEvents(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_orphan_test")
	ctx := context.Background()

	oldTime := time.Now().UTC().Add(-10 * time.Minute)
	eventsCol := db.Collection("events")

	// Genuinely orphaned: old received_at, no deliveries, no fanout metadata.
	eventID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":         eventID,
		"source":      "test",
		"event_type":  "user.created",
		"received_at": oldTime,
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Has fanout_status — should NOT be treated as orphan (handled by
	// FindIncompleteFanout or already terminal).
	fannedOutID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":                   fannedOutID,
		"source":                "test",
		"event_type":            "user.created",
		"received_at":           oldTime,
		"fanout_status":         "completed",
		"fanout_expected_count": 0,
	}); err != nil {
		t.Fatalf("insert no-destination event: %v", err)
	}

	// Has a delivery — should not be orphaned.
	normalEventID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":         normalEventID,
		"source":      "test",
		"event_type":  "user.created",
		"received_at": oldTime,
	}); err != nil {
		t.Fatalf("insert normal event: %v", err)
	}
	if _, err := db.Collection("deliveries").InsertOne(ctx, bson.M{
		"event_id": normalEventID,
		"status":   "pending",
	}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	// Recent event — should not be orphaned (below threshold).
	recentEventID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":         recentEventID,
		"source":      "test",
		"event_type":  "user.created",
		"received_at": time.Now().UTC().Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("insert recent event: %v", err)
	}

	store := NewStore(db)
	threshold := time.Now().UTC().Add(-5 * time.Minute)

	orphaned, err := store.FindOrphanedEvents(ctx, threshold)
	if err != nil {
		t.Fatalf("find orphaned: %v", err)
	}
	if len(orphaned) != 1 {
		t.Fatalf("expected 1 orphaned event, got %d", len(orphaned))
	}
	if orphaned[0].ID != eventID {
		t.Errorf("expected ID %s, got %s", eventID.Hex(), orphaned[0].ID.Hex())
	}
}

func TestFindIncompleteFanoutIncompleteStatus(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_fanout_incomplete")
	ctx := context.Background()

	eventsCol := db.Collection("events")

	// fanout_status: "incomplete" — should be returned regardless of age.
	incompleteID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":                   incompleteID,
		"source":                "test",
		"event_type":            "user.created",
		"received_at":           time.Now().UTC(),
		"fanout_status":         "incomplete",
		"fanout_expected_count": 3,
	}); err != nil {
		t.Fatalf("insert incomplete event: %v", err)
	}

	// fanout_status: "completed" — should NOT be returned.
	completedID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":                   completedID,
		"source":                "test",
		"event_type":            "user.created",
		"received_at":           time.Now().UTC().Add(-1 * time.Hour),
		"fanout_status":         "completed",
		"fanout_expected_count": 0,
	}); err != nil {
		t.Fatalf("insert completed event: %v", err)
	}

	store := NewStore(db)
	threshold := time.Now().UTC().Add(-5 * time.Minute)

	recovered, err := store.FindIncompleteFanout(ctx, threshold)
	if err != nil {
		t.Fatalf("FindIncompleteFanout: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("expected 1 incomplete event, got %d", len(recovered))
	}
	if recovered[0].ID != incompleteID {
		t.Errorf("expected ID %s, got %s", incompleteID.Hex(), recovered[0].ID.Hex())
	}
}

func TestFindIncompleteFanoutInProgressOldEnough(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_fanout_inprogress")
	ctx := context.Background()

	eventsCol := db.Collection("events")
	oldTime := time.Now().UTC().Add(-10 * time.Minute)

	// in_progress and old — should be returned (ingestion crashed mid-fanout).
	staleID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":           staleID,
		"source":        "test",
		"event_type":    "user.created",
		"received_at":   oldTime,
		"fanout_status": "in_progress",
	}); err != nil {
		t.Fatalf("insert stale event: %v", err)
	}

	// in_progress and recent — should NOT be returned (still running).
	activeID := bson.NewObjectID()
	if _, err := eventsCol.InsertOne(ctx, bson.M{
		"_id":           activeID,
		"source":        "test",
		"event_type":    "user.created",
		"received_at":   time.Now().UTC().Add(-30 * time.Second),
		"fanout_status": "in_progress",
	}); err != nil {
		t.Fatalf("insert active event: %v", err)
	}

	store := NewStore(db)
	threshold := time.Now().UTC().Add(-5 * time.Minute)

	recovered, err := store.FindIncompleteFanout(ctx, threshold)
	if err != nil {
		t.Fatalf("FindIncompleteFanout: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("expected 1 stale in-progress event, got %d", len(recovered))
	}
	if recovered[0].ID != staleID {
		t.Errorf("expected ID %s, got %s", staleID.Hex(), recovered[0].ID.Hex())
	}
}

func TestResetExpiredLeases(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_reset_test")
	ctx := context.Background()

	now := time.Now().UTC()
	deliveryID := bson.NewObjectID()

	if _, err := db.Collection("deliveries").InsertOne(ctx, bson.M{
		"_id":         deliveryID,
		"event_id":    bson.NewObjectID(),
		"endpoint_id": bson.NewObjectID(),
		"status":      "processing",
		"lease_until": now.Add(-1 * time.Minute),
		"lease_token": "old-token",
		"attempts":    1,
		"updated_at":  now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	store := NewStore(db)
	modified, err := store.ResetExpiredLeases(ctx, now)
	if err != nil {
		t.Fatalf("ResetExpiredLeases: %v", err)
	}
	if modified != 1 {
		t.Fatalf("expected 1 modified, got %d", modified)
	}

	var got bson.M
	if err := db.Collection("deliveries").
		FindOne(ctx, bson.M{"_id": deliveryID}).
		Decode(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got["status"] != "pending" {
		t.Errorf("status = %v, want pending", got["status"])
	}
	if _, present := got["lease_token"]; present {
		t.Errorf("lease_token should be unset, got %v", got["lease_token"])
	}
	if _, present := got["lease_until"]; present {
		t.Errorf("lease_until should be unset, got %v", got["lease_until"])
	}
}

func TestResetExpiredLeasesSkipsActiveLease(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_active_lease_test")
	ctx := context.Background()

	now := time.Now().UTC()
	deliveryID := bson.NewObjectID()

	if _, err := db.Collection("deliveries").InsertOne(ctx, bson.M{
		"_id":         deliveryID,
		"status":      "processing",
		"lease_until": now.Add(30 * time.Second),
		"lease_token": "fresh-token",
		"updated_at":  now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	store := NewStore(db)
	modified, err := store.ResetExpiredLeases(ctx, now)
	if err != nil {
		t.Fatalf("ResetExpiredLeases: %v", err)
	}
	if modified != 0 {
		t.Fatalf("expected 0 modified, got %d", modified)
	}

	var got bson.M
	if err := db.Collection("deliveries").
		FindOne(ctx, bson.M{"_id": deliveryID}).
		Decode(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got["status"] != "processing" {
		t.Errorf("status = %v, want processing", got["status"])
	}
	if got["lease_token"] != "fresh-token" {
		t.Errorf("lease_token = %v, want fresh-token", got["lease_token"])
	}
}

func TestResetExpiredLeasesSkipsNonProcessing(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_delivered_test")
	ctx := context.Background()

	now := time.Now().UTC()
	deliveryID := bson.NewObjectID()

	if _, err := db.Collection("deliveries").InsertOne(ctx, bson.M{
		"_id":         deliveryID,
		"status":      "delivered",
		"lease_until": now.Add(-1 * time.Minute),
		"lease_token": "stale-token",
		"updated_at":  now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	store := NewStore(db)
	modified, err := store.ResetExpiredLeases(ctx, now)
	if err != nil {
		t.Fatalf("ResetExpiredLeases: %v", err)
	}
	if modified != 0 {
		t.Fatalf("expected 0 modified, got %d", modified)
	}

	var got bson.M
	if err := db.Collection("deliveries").
		FindOne(ctx, bson.M{"_id": deliveryID}).
		Decode(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got["status"] != "delivered" {
		t.Errorf("status = %v, want delivered", got["status"])
	}
}

func TestResetExpiredLeasesConcurrentReclaim(t *testing.T) {
	db := newReconcilerTestDB(t, "relay_reconciler_race_test")
	ctx := context.Background()

	now := time.Now().UTC()
	deliveryID := bson.NewObjectID()

	if _, err := db.Collection("deliveries").InsertOne(ctx, bson.M{
		"_id":         deliveryID,
		"status":      "processing",
		"lease_until": now.Add(-1 * time.Minute),
		"lease_token": "old-token",
		"updated_at":  now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := db.Collection("deliveries").UpdateOne(
		ctx,
		bson.M{"_id": deliveryID},
		bson.M{"$set": bson.M{
			"lease_until": now.Add(30 * time.Second),
			"lease_token": "new-token",
		}},
	); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	store := NewStore(db)
	modified, err := store.ResetExpiredLeases(ctx, now)
	if err != nil {
		t.Fatalf("ResetExpiredLeases: %v", err)
	}
	if modified != 0 {
		t.Fatalf("expected 0 modified, got %d", modified)
	}

	var got bson.M
	if err := db.Collection("deliveries").
		FindOne(ctx, bson.M{"_id": deliveryID}).
		Decode(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got["status"] != "processing" {
		t.Errorf("status = %v, want processing", got["status"])
	}
	if got["lease_token"] != "new-token" {
		t.Errorf("lease_token = %v, want new-token", got["lease_token"])
	}
}