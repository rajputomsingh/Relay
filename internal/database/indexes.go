package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

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
