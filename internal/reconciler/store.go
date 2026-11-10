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

// FindOrphanedEvents returns events older than the threshold that have no
// deliveries AND no fan-out metadata recorded.
func (s *Store) FindOrphanedEvents(ctx context.Context, threshold time.Time) ([]EventRecord, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{
			"received_at":   bson.M{"$lt": threshold},
			"fanout_status": bson.M{"$exists": false},
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

// FindIncompleteFanout returns events whose fan-out is incomplete:
//   - fanout_status == "incomplete" (some delivery upserts failed), or
//   - fanout_status == "in_progress" and received_at is older than threshold
func (s *Store) FindIncompleteFanout(ctx context.Context, threshold time.Time) ([]EventRecord, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{
			"$or": bson.A{
				bson.M{"fanout_status": "incomplete"},
				bson.M{
					"fanout_status": "in_progress",
					"received_at":   bson.M{"$lt": threshold},
				},
			},
		}}},
		bson.D{{Key: "$project", Value: bson.M{
			"source":     1,
			"event_type": 1,
		}}},
	}

	cursor, err := s.eventsCol.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate incomplete fanout events: %w", err)
	}
	defer cursor.Close(ctx)

	var events []EventRecord
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decode incomplete fanout events: %w", err)
	}

	return events, nil
}

// ResetExpiredLeases atomically transitions processing deliveries whose
// lease has expired back to pending.
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

// FindStaleParentStates returns event IDs whose delivery_status disagrees
// with their child delivery states. The caller is expected to invoke the
// lifecycle aggregator for each returned event.
//
// A parent is stale when:
//   - delivery_status is pending, processing, or managed, but every
//     applicable delivery is terminal (delivered or failed); OR
//   - delivery_status is delivered but at least one child is failed; OR
//   - delivery_status is failed but every child is delivered.
//
// Events with fanout_status in_progress or incomplete are skipped: the
// fan-out recovery step handles them first. Events with delivery_status
// "disabled" or "no_destination" are excluded — terminal and independent
// of child deliveries.
//
// Only events whose received_at is older than (now - 1m) are considered,
// to avoid racing with freshly ingested events whose fan-out has not yet
// completed.
func (s *Store) FindStaleParentStates(
	ctx context.Context,
	now time.Time,
) ([]bson.ObjectID, error) {
	cutoff := now.Add(-1 * time.Minute)

	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{
			"received_at":     bson.M{"$lt": cutoff},
			"delivery_status": bson.M{"$nin": []string{"disabled", "no_destination"}},
			"fanout_status":   "completed",
		}}},
		bson.D{{Key: "$lookup", Value: bson.M{
			"from":         "deliveries",
			"localField":   "_id",
			"foreignField": "event_id",
			"as":           "children",
		}}},
		bson.D{{Key: "$addFields", Value: bson.M{
			"delivered": bson.M{"$size": bson.M{"$filter": bson.M{
				"input": "$children",
				"as":    "c",
				"cond":  bson.M{"$eq": bson.A{"$$c.status", "delivered"}},
			}}},
			"failed": bson.M{"$size": bson.M{"$filter": bson.M{
				"input": "$children",
				"as":    "c",
				"cond":  bson.M{"$eq": bson.A{"$$c.status", "failed"}},
			}}},
			"pendingOrProcessing": bson.M{"$size": bson.M{"$filter": bson.M{
				"input": "$children",
				"as":    "c",
				"cond": bson.M{"$in": bson.A{
					"$$c.status",
					bson.A{"pending", "processing"},
				}},
			}}},
			"total": bson.M{"$size": "$children"},
		}}},
		bson.D{{Key: "$match", Value: bson.M{
			"pendingOrProcessing": 0,
			"total":               bson.M{"$gt": 0},
			"$expr": bson.M{"$or": bson.A{
				bson.M{"$in": bson.A{
					"$delivery_status",
					bson.A{"pending", "processing", "managed"},
				}},
				bson.M{"$and": bson.A{
					bson.M{"$eq": bson.A{"$delivery_status", "delivered"}},
					bson.M{"$gt": bson.A{"$failed", 0}},
				}},
				bson.M{"$and": bson.A{
					bson.M{"$eq": bson.A{"$delivery_status", "failed"}},
					bson.M{"$eq": bson.A{"$failed", 0}},
				}},
			}},
		}}},
		bson.D{{Key: "$project", Value: bson.M{"_id": 1}}},
	}

	cursor, err := s.eventsCol.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate stale parent states: %w", err)
	}
	defer cursor.Close(ctx)

	var ids []bson.ObjectID
	for cursor.Next(ctx) {
		var row struct {
			ID bson.ObjectID `bson:"_id"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode stale parent: %w", err)
		}
		ids = append(ids, row.ID)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale parent states: %w", err)
	}

	return ids, nil
}
