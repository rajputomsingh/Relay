package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Fanout creates per-endpoint delivery records for accepted events.
type Fanout struct {
	events     *mongo.Collection
	endpoints  *mongo.Collection
	deliveries *mongo.Collection
	logger     *slog.Logger
}

// NewFanout constructs a Fanout service.
func NewFanout(
	events *mongo.Collection,
	endpoints *mongo.Collection,
	deliveries *mongo.Collection,
	logger *slog.Logger,
) *Fanout {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fanout{
		events:     events,
		endpoints:  endpoints,
		deliveries: deliveries,
		logger:     logger,
	}
}

// CreateDeliveries inserts one delivery per matching enabled endpoint.
// It is safe to call more than once for the same event:
// deliveries are upserted on (event_id, endpoint_id).
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
			NextAttemptAt: nil, // eligible immediately
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		filter := bson.M{
			"event_id":    eventID,
			"endpoint_id": ep.ID,
		}

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
			ctx,
			filter,
			update,
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			// Do not fail the whole fan-out for one endpoint; log and continue.
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

	if matched == 0 {
		f.logger.Warn(
			"event accepted with no matching endpoints",
			"event_id", eventID.Hex(),
			"source", source,
			"event_type", eventType,
		)
		return nil
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
// It is only used by callers that wish to distinguish this case.
var ErrNoEndpoints = errors.New("no enabled endpoints")
