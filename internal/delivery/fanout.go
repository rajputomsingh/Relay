package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"github.com/rajputomsingh/relay/internal/lifecycle"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Fanout creates per-endpoint delivery records for accepted events.
type Fanout struct {
	events     *mongo.Collection
	endpoints  *mongo.Collection
	deliveries *mongo.Collection
	aggregator *lifecycle.Aggregator
	logger     *slog.Logger
}

// NewFanout constructs a Fanout service.
func NewFanout(
	events *mongo.Collection,
	endpoints *mongo.Collection,
	deliveries *mongo.Collection,
	aggregator *lifecycle.Aggregator,
	logger *slog.Logger,
) *Fanout {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fanout{
		events:     events,
		endpoints:  endpoints,
		deliveries: deliveries,
		aggregator: aggregator,
		logger:     logger,
	}
}

// Fan-out status values persisted on the event document.
const (
	FanoutStatusInProgress = "in_progress"
	FanoutStatusCompleted  = "completed"
	FanoutStatusIncomplete = "incomplete"
)

// CreateDeliveries inserts one delivery per matching enabled endpoint
// and records fan-out metadata on the event.
//
// The lifecycle aggregator is invoked at the end so the parent
// delivery_status transitions to no_destination (zero matches) or
// processing (fan-out complete, deliveries pending). Aggregator failures
// are logged but do not fail the fan-out; the reconciler repairs stale
// parent states.
func (f *Fanout) CreateDeliveries(
	ctx context.Context,
	eventID bson.ObjectID,
	source string,
	eventType string,
) error {
	cursor, err := f.endpoints.Find(
		ctx,
		bson.M{"enabled": true},
		options.Find().SetProjection(bson.M{
			"_id":          1,
			"event_filter": 1,
			"max_attempts": 1,
		}),
	)
	if err != nil {
		return fmt.Errorf("load enabled endpoints: %w", err)
	}
	defer cursor.Close(ctx)

	now := time.Now().UTC()

	matched := 0
	created := 0
	upsertFailed := 0

	for cursor.Next(ctx) {
		var ep endpoints.Endpoint
		if err := cursor.Decode(&ep); err != nil {
			return fmt.Errorf("decode endpoint: %w", err)
		}

		if !ep.EventFilter.Matches(source, eventType) {
			continue
		}
		matched++

		delivery := Delivery{
			EventID:       eventID,
			EndpointID:    ep.ID,
			Status:        StatusPending,
			Attempts:      0,
			MaxAttempts:   ep.MaxAttempts,
			NextAttemptAt: nil,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		filter := bson.M{"event_id": eventID, "endpoint_id": ep.ID}
		update := bson.M{
			"$setOnInsert": bson.M{
				"status":       delivery.Status,
				"attempts":     delivery.Attempts,
				"max_attempts": delivery.MaxAttempts,
				"created_at":   delivery.CreatedAt,
				"updated_at":   delivery.UpdatedAt,
			},
		}

		result, err := f.deliveries.UpdateOne(
			ctx, filter, update, options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			upsertFailed++
			f.logger.Error(
				"fanout delivery upsert failed",
				"event_id", eventID.Hex(),
				"endpoint_id", ep.ID.Hex(),
				"error_type", fmt.Sprintf("%T", err),
			)
			continue
		}

		if result.UpsertedCount > 0 {
			created++
		}
	}

	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate endpoints: %w", err)
	}

	fanoutStatus := FanoutStatusCompleted
	if upsertFailed > 0 {
		fanoutStatus = FanoutStatusIncomplete
	}

	setFields := bson.M{
		"fanout_status":         fanoutStatus,
		"fanout_expected_count": matched,
		"fanout_completed_at":   now,
	}
	if _, err := f.events.UpdateOne(
		ctx,
		bson.M{"_id": eventID},
		bson.M{"$set": setFields},
	); err != nil {
		return fmt.Errorf("record fanout status: %w", err)
	}

	// Invoke the lifecycle aggregator so parent status reflects fan-out outcome.
	if f.aggregator != nil {
		aggregateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := f.aggregator.AggregateEvent(aggregateCtx, eventID); err != nil {
			f.logger.Error(
				"lifecycle aggregation failed after fanout",
				"event_id", eventID.Hex(),
				"error_type", fmt.Sprintf("%T", err),
			)
		}
		cancel()
	}

	if matched == 0 {
		f.logger.Warn(
			"event accepted with no matching endpoints",
			"event_id", eventID.Hex(),
			"source", source,
			"event_type", eventType,
		)
		return nil
	}

	if upsertFailed > 0 {
		f.logger.Warn(
			"fanout completed with failures",
			"event_id", eventID.Hex(),
			"matched", matched,
			"created", created,
			"failed", upsertFailed,
		)
		return fmt.Errorf(
			"fanout incomplete: %d of %d delivery upserts failed",
			upsertFailed, matched,
		)
	}

	f.logger.Info(
		"event fanned out to endpoints",
		"event_id", eventID.Hex(),
		"matched", matched,
		"created", created,
	)

	return nil
}

// ErrNoEndpoints indicates that fan-out was invoked with an empty endpoint set.
var ErrNoEndpoints = errors.New("no enabled endpoints")
