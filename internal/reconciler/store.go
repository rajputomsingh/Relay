package reconciler

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
			"created_at": bson.M{"$lt": threshold},
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

// FindStuckDeliveries returns deliveries stuck in 'processing' state past the lease threshold.
func (s *Store) FindStuckDeliveries(ctx context.Context, threshold time.Time) ([]bson.ObjectID, error) {
	filter := bson.M{
		"status":     "processing",
		"updated_at": bson.M{"$lt": threshold},
	}

	opts := options.Find().SetProjection(bson.M{"_id": 1})

	cursor, err := s.deliveriesCol.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find stuck deliveries: %w", err)
	}
	defer cursor.Close(ctx)

	var ids []bson.ObjectID
	for cursor.Next(ctx) {
		var row struct {
			ID bson.ObjectID `bson:"_id"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode stuck delivery: %w", err)
		}
		ids = append(ids, row.ID)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate stuck deliveries: %w", err)
	}

	return ids, nil
}

// ResetDeliveriesToPending updates the status of the given deliveries to 'pending'.
func (s *Store) ResetDeliveriesToPending(ctx context.Context, ids []bson.ObjectID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	filter := bson.M{"_id": bson.M{"$in": ids}}
	update := bson.M{
		"$set": bson.M{
			"status":      "pending",
			"updated_at":  time.Now().UTC(),
			"lease_token": nil,
		},
	}

	result, err := s.deliveriesCol.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("reset stuck deliveries: %w", err)
	}

	return result.ModifiedCount, nil
}
