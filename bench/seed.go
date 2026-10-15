package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// seedData identifies the documents inserted for one benchmark run.
type seedData struct {
	EndpointID  bson.ObjectID
	DeliveryIDs []bson.ObjectID
	EventIDs    []bson.ObjectID
}

// seed inserts one endpoint, N events, and N pending deliveries into
// the given database. It returns the identifiers so the harness can
// verify outcomes after the run.
//
// The documents match the exact schema the production worker expects.
// No shortcut fields are invented.
func seed(
	ctx context.Context,
	db *mongo.Database,
	endpointURL string,
	count int,
	maxAttempts int,
) (seedData, error) {
	eventsCol := db.Collection("events")
	endpointsCol := db.Collection("endpoints")
	deliveriesCol := db.Collection("deliveries")

	now := time.Now().UTC()

	endpointID := bson.NewObjectID()
	endpoint := bson.M{
		"_id":            endpointID,
		"name":           "bench-" + endpointID.Hex(),
		"url":            endpointURL,
		"enabled":        true,
		"max_attempts":   maxAttempts,
		"rate_limit_rps": 0,
		"created_at":     now,
		"updated_at":     now,
	}

	if _, err := endpointsCol.InsertOne(ctx, endpoint); err != nil {
		return seedData{}, fmt.Errorf("insert endpoint: %w", err)
	}

	// Batch-insert events and deliveries to keep seeding fast.
	const batchSize = 500

	data := seedData{
		EndpointID:  endpointID,
		DeliveryIDs: make([]bson.ObjectID, 0, count),
		EventIDs:    make([]bson.ObjectID, 0, count),
	}

	for offset := 0; offset < count; offset += batchSize {
		end := offset + batchSize
		if end > count {
			end = count
		}

		events := make([]any, 0, end-offset)
		deliveries := make([]any, 0, end-offset)

		for i := offset; i < end; i++ {
			eventID := bson.NewObjectID()
			deliveryID := bson.NewObjectID()

			events = append(events, bson.M{
				"_id":             eventID,
				"source":          "bench",
				"event_type":      "bench.delivery",
				"data":            bson.M{"n": i},
				"idempotency_key": "bench-" + eventID.Hex(),
				"received_at":     now,
				"status":          "accepted",
			})

			deliveries = append(deliveries, bson.M{
				"_id":          deliveryID,
				"event_id":     eventID,
				"endpoint_id":  endpointID,
				"status":       "pending",
				"attempts":     0,
				"max_attempts": maxAttempts,
				"created_at":   now,
				"updated_at":   now,
			})

			data.EventIDs = append(data.EventIDs, eventID)
			data.DeliveryIDs = append(data.DeliveryIDs, deliveryID)
		}

		if _, err := eventsCol.InsertMany(ctx, events); err != nil {
			return seedData{}, fmt.Errorf("insert events batch: %w", err)
		}
		if _, err := deliveriesCol.InsertMany(ctx, deliveries); err != nil {
			return seedData{}, fmt.Errorf("insert deliveries batch: %w", err)
		}
	}

	return data, nil
}

// ensureIndexes creates the indexes the worker relies on. Without
// these, claims and lease recovery perform full collection scans and
// benchmark numbers become meaningless.
func ensureIndexes(
	ctx context.Context,
	db *mongo.Database,
) error {
	deliveriesCol := db.Collection("deliveries")
	attemptsCol := db.Collection("delivery_attempts")

	_, err := deliveriesCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "event_id", Value: 1},
				{Key: "endpoint_id", Value: 1},
			},
			Options: options.Index().
				SetUnique(true).
				SetName("uniq_delivery_event_endpoint"),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "next_attempt_at", Value: 1},
			},
			Options: options.Index().
				SetName("idx_delivery_due"),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "lease_until", Value: 1},
			},
			Options: options.Index().
				SetName("idx_delivery_lease"),
		},
	})
	if err != nil {
		return fmt.Errorf("create delivery indexes: %w", err)
	}

	_, err = attemptsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "event_id", Value: 1},
			{Key: "started_at", Value: -1},
		},
		Options: options.Index().
			SetName("idx_attempts_event_started"),
	})
	if err != nil {
		return fmt.Errorf("create attempt indexes: %w", err)
	}

	return nil
}

// dropBenchmarkDatabase removes the benchmark database. It is safe to
// call even when the database does not exist.
func dropBenchmarkDatabase(
	ctx context.Context,
	db *mongo.Database,
) error {
	if err := db.Drop(ctx); err != nil {
		return fmt.Errorf("drop benchmark database: %w", err)
	}
	return nil
}

// uniqueDatabaseName generates a per-run database name so concurrent
// or historical benchmark runs never interfere.
func uniqueDatabaseName() string {
	return fmt.Sprintf("relay_bench_%d", time.Now().UnixNano())
}

// errNoSeedData indicates a caller used seedData before it was
// populated. It exists only to catch programming mistakes.
var errNoSeedData = errors.New("seed data has not been populated")
