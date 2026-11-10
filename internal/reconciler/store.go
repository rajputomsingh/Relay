package reconciler

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Store handles the database operations required by the reconciler.
type Store struct {
	eventsCol     *mongo.Collection
	deliveriesCol *mongo.Collection
}

// NewStore creates a new reconciler store.
func NewStore(db *mongo.Database) *Store {
	return &Store{
		eventsCol:     db.Collection("events"),
		deliveriesCol: db.Collection("deliveries"),
	}
}

// EventRecord represents the minimal event data needed to re-trigger fan-out.
type EventRecord struct {
	ID        bson.ObjectID `bson:"_id"`
	Source    string        `bson:"source"`
	EventType string        `bson:"event_type"`
}

// FindOrphanedEvents returns events older than the threshold that have no deliveries.
func (s *Store) FindOrphanedEvents(ctx context.Context, threshold time.Time) ([]EventRecord, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{
			"received_at": bson.M{"$lt": threshold},
		}}},
		bson.D{{Key: "$lookup", Value: bson.M{
			"from":         "deliveries",
			"localField":   "_id",
			"foreignField": "event_id",
			"as":           "matching_deliveries",
		}}},
		bson.D{{Key: "$match", Value: bson.M{
			"matching_deliveries": bson.M{"$size": 0},
		}}},
		bson.D{{Key: "$project", Value: bson.M{
			"source":     1,
			"event_type": 1,
		}}},
	}

	cursor, err := s.eventsCol.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate orphaned events: %w", err)
	}
	defer cursor.Close(ctx)

	var events []EventRecord
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decode orphaned events: %w", err)
	}

	return events, nil
}

// ResetExpiredLeases atomically transitions processing deliveries whose
// lease has expired back to pending.
//
// The update is conditional on the delivery still being in `processing`
// status with `lease_until <= now`. A worker that reclaimed or renewed the
// lease between discovery and this update will not be affected, because
// the filter no longer matches its row.
//
// Both lease_token and lease_until are cleared so that inspection and
// metrics do not report a lease for a delivery no worker owns.
func (s *Store) ResetExpiredLeases(ctx context.Context, now time.Time) (int64, error) {
	filter := bson.M{
		"status":      "processing",
		"lease_until": bson.M{"$lte": now},
	}
	update := bson.M{
		"$set": bson.M{
			"status":     "pending",
			"updated_at": now,
		},
		"$unset": bson.M{
			"lease_token": "",
			"lease_until": "",
		},
	}

	result, err := s.deliveriesCol.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("reset expired leases: %w", err)
	}

	return result.ModifiedCount, nil
}
