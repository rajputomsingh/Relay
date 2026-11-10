// Package lifecycle derives the parent event's delivery_status from its
// child delivery records.
//
// The aggregator is the sole writer of events.delivery_status. Ingestion
// writes an initial placeholder, but only the aggregator can move the
// parent to a terminal state. Workers, replay, and the reconciler must
// invoke the aggregator rather than writing events.delivery_status
// directly. See docs/architecture/event-delivery-lifecycle.md §5 and §12.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Parent event delivery_status values.
const (
	StatusDisabled      = "disabled"
	StatusManaged       = "managed"
	StatusPending       = "pending"
	StatusProcessing    = "processing"
	StatusDelivered     = "delivered"
	StatusFailed        = "failed"
	StatusNoDestination = "no_destination"
)

// Aggregator recomputes parent delivery_status from child deliveries.
type Aggregator struct {
	events     *mongo.Collection
	deliveries *mongo.Collection
}

// NewAggregator creates an Aggregator backed by the given collections.
func NewAggregator(
	events *mongo.Collection,
	deliveries *mongo.Collection,
) *Aggregator {
	return &Aggregator{
		events:     events,
		deliveries: deliveries,
	}
}

// AggregateEvent recomputes events.delivery_status for a single event and
// writes the result.
//
// Idempotent and safe to run concurrently. A concurrent call may write a
// stale intermediate value (pending vs processing), but the final call
// after all deliveries settle produces the correct terminal state, and the
// reconciler periodically recomputes any parent whose state does not match
// its child deliveries.
//
// If the event does not exist, returns mongo.ErrNoDocuments.
// If the event is disabled, the aggregator does not change its status.
func (a *Aggregator) AggregateEvent(
	ctx context.Context,
	eventID bson.ObjectID,
) error {
	var event struct {
		ID             bson.ObjectID `bson:"_id"`
		DeliveryStatus string        `bson:"delivery_status"`
		FanoutStatus   string        `bson:"fanout_status"`
		ExpectedCount  int           `bson:"fanout_expected_count"`
	}
	if err := a.events.FindOne(
		ctx, bson.M{"_id": eventID},
	).Decode(&event); err != nil {
		return err
	}

	// Disabled events terminate independently of deliveries.
	if event.DeliveryStatus == StatusDisabled {
		return nil
	}

	now := time.Now().UTC()

	// Zero matching endpoints: fan-out completed with expected count 0.
	// This is a terminal state and requires no delivery work.
	if event.FanoutStatus == "completed" && event.ExpectedCount == 0 {
		return a.writeStatus(ctx, eventID, StatusNoDestination, now, nil)
	}

	// Fan-out incomplete: do not attempt to derive a terminal status yet.
	// The reconciler will re-run fan-out; once that completes, this method
	// is called again and produces a terminal state.
	if event.FanoutStatus == "in_progress" || event.FanoutStatus == "incomplete" {
		return a.writeStatus(ctx, eventID, StatusProcessing, now, nil)
	}

	// Aggregate child delivery states.
	cursor, err := a.deliveries.Aggregate(
		ctx,
		mongo.Pipeline{
			bson.D{{Key: "$match", Value: bson.M{"event_id": eventID}}},
			bson.D{{Key: "$group", Value: bson.M{
				"_id":   "$status",
				"count": bson.M{"$sum": 1},
			}}},
		},
	)
	if err != nil {
		return fmt.Errorf("aggregate deliveries: %w", err)
	}
	defer func() {
		_ = cursor.Close(ctx)
	}()

	counts := make(map[string]int)
	total := 0
	for cursor.Next(ctx) {
		var row struct {
			Status string `bson:"_id"`
			Count  int    `bson:"count"`
		}
		if err := cursor.Decode(&row); err != nil {
			return fmt.Errorf("decode delivery aggregate: %w", err)
		}
		counts[row.Status] = row.Count
		total += row.Count
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate delivery aggregate: %w", err)
	}

	// Zero child deliveries and fan-out not marked complete: still processing.
	// The reconciler will recover the missing fan-out.
	if total == 0 {
		return a.writeStatus(ctx, eventID, StatusProcessing, now, nil)
	}

	delivered := counts["delivered"]
	failed := counts["failed"]
	pending := counts["pending"]
	processing := counts["processing"]

	switch {
	case processing > 0:
		return a.writeStatus(ctx, eventID, StatusProcessing, now, nil)
	case pending > 0:
		return a.writeStatus(ctx, eventID, StatusPending, now, nil)
	case delivered == total:
		return a.writeStatus(ctx, eventID, StatusDelivered, now, &now)
	case delivered+failed == total && failed > 0:
		return a.writeStatus(ctx, eventID, StatusFailed, now, nil)
	default:
		return a.writeStatus(ctx, eventID, StatusProcessing, now, nil)
	}
}

// writeStatus persists the aggregated status with the correct timestamp.
func (a *Aggregator) writeStatus(
	ctx context.Context,
	eventID bson.ObjectID,
	status string,
	now time.Time,
	deliveredAt *time.Time,
) error {
	set := bson.M{
		"delivery_status": status,
		"updated_at":      now,
	}
	unset := bson.M{}

	switch status {
	case StatusDelivered:
		if deliveredAt != nil {
			set["delivered_at"] = *deliveredAt
		}
		unset["failed_at"] = ""
	case StatusFailed:
		set["failed_at"] = now
		unset["delivered_at"] = ""
	case StatusNoDestination:
		unset["failed_at"] = ""
		unset["delivered_at"] = ""
	default:
		unset["failed_at"] = ""
		unset["delivered_at"] = ""
	}

	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}

	result, err := a.events.UpdateOne(
		ctx,
		bson.M{
			"_id":             eventID,
			"delivery_status": bson.M{"$ne": StatusDisabled},
		},
		update,
	)
	if err != nil {
		return fmt.Errorf("write parent delivery_status: %w", err)
	}
	if result.MatchedCount == 0 {
		return errors.New(
			"write parent delivery_status: event disappeared or was disabled",
		)
	}

	return nil
}
