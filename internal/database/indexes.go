package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// RequiredIndexes names every index that production code relies on. Used by
// VerifyIndexes at startup to detect drift (e.g., someone dropped an index
// during a maintenance window and forgot to recreate it).
var RequiredIndexes = map[string][]string{
	"endpoints": {
		"uniq_endpoint_name",
		"idx_endpoint_enabled",
	},
	"deliveries": {
		"uniq_delivery_event_endpoint",
		"idx_delivery_due",
		"idx_delivery_lease",
		"idx_delivery_endpoint_status",
		"idx_delivery_event",
	},
	"delivery_attempts": {
		"idx_attempts_endpoint_started",
		"idx_attempts_event_endpoint_started",
	},
}

// EnsureMultiEndpointIndexes creates the indexes required by the
// endpoints and deliveries collections.
func EnsureMultiEndpointIndexes(
	ctx context.Context,
	db *mongo.Database,
) error {
	endpoints := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")

	endpointIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "name", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetName("uniq_endpoint_name"),
		},
		{
			Keys: bson.D{{Key: "enabled", Value: 1}},
			Options: options.Index().
				SetName("idx_endpoint_enabled"),
		},
	}

	deliveryIndexes := []mongo.IndexModel{
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
		{
			Keys: bson.D{
				{Key: "endpoint_id", Value: 1},
				{Key: "status", Value: 1},
			},
			Options: options.Index().
				SetName("idx_delivery_endpoint_status"),
		},
		{
			Keys: bson.D{
				{Key: "endpoint_id", Value: 1},
				{Key: "status", Value: 1},
				{Key: "updated_at", Value: -1},
				{Key: "_id", Value: -1},
			},
			Options: options.Index().
				SetName("idx_delivery_endpoint_status_updated"),
		},
		{
			Keys: bson.D{{Key: "event_id", Value: 1}},
			Options: options.Index().
				SetName("idx_delivery_event"),
		},
	}

	attemptIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "endpoint_id", Value: 1},
				{Key: "started_at", Value: -1},
			},
			Options: options.Index().
				SetName("idx_attempts_endpoint_started"),
		},
		{
			Keys: bson.D{
				{Key: "event_id", Value: 1},
				{Key: "endpoint_id", Value: 1},
				{Key: "started_at", Value: -1},
			},
			Options: options.Index().
				SetName("idx_attempts_event_endpoint_started"),
		},
	}

	if _, err := endpoints.Indexes().CreateMany(ctx, endpointIndexes); err != nil {
		return fmt.Errorf("create endpoint indexes: %w", err)
	}
	if _, err := deliveries.Indexes().CreateMany(ctx, deliveryIndexes); err != nil {
		return fmt.Errorf("create delivery indexes: %w", err)
	}
	if _, err := attempts.Indexes().CreateMany(ctx, attemptIndexes); err != nil {
		return fmt.Errorf("create attempt endpoint indexes: %w", err)
	}

	return nil
}

// VerifyIndexes returns the list of required indexes that are missing from
// the database. If the returned slice is empty, every required index exists.
//
// Intended for readiness checks and startup logging. Does not create
// indexes — that is EnsureMultiEndpointIndexes's job.
func VerifyIndexes(
	ctx context.Context,
	db *mongo.Database,
) ([]string, error) {
	var missing []string

	for collName, required := range RequiredIndexes {
		cursor, err := db.Collection(collName).Indexes().List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list indexes for %s: %w", collName, err)
		}

		existing := make(map[string]struct{})
		for cursor.Next(ctx) {
			var doc bson.M
			if err := cursor.Decode(&doc); err != nil {
				_ = cursor.Close(ctx)
				return nil, fmt.Errorf("decode index for %s: %w", collName, err)
			}
			if name, ok := doc["name"].(string); ok {
				existing[name] = struct{}{}
			}
		}
		if err := cursor.Err(); err != nil {
			_ = cursor.Close(ctx)
			return nil, fmt.Errorf("iterate indexes for %s: %w", collName, err)
		}
		_ = cursor.Close(ctx)

		for _, name := range required {
			if _, ok := existing[name]; !ok {
				missing = append(missing, fmt.Sprintf("%s.%s", collName, name))
			}
		}
	}

	return missing, nil
}

// indexChecker adapts VerifyIndexes to the handlers.indexChecker interface
// (which requires a MissingIndexes method). Allows main.go to wire the
// checker without importing handler internals into the database package.
type indexChecker struct {
	db *mongo.Database
}

// NewIndexChecker returns an object suitable for
// handlers.HealthHandler.WithIndexChecker.
func NewIndexChecker(db *mongo.Database) *indexChecker {
	return &indexChecker{db: db}
}

// MissingIndexes returns the list of required indexes that are not present
// in the database. An empty slice (nil is also fine) means readiness should
// pass.
func (c *indexChecker) MissingIndexes(ctx context.Context) ([]string, error) {
	return VerifyIndexes(ctx, c.db)
}
