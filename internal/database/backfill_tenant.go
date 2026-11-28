package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// BackfillLegacyTenant stamps tenant_id on pre-existing documents that
// lack it. Single-tenant deployments map all legacy data to legacyTenant.
// Idempotent: only matches docs where tenant_id is absent.
func BackfillLegacyTenant(
	ctx context.Context,
	db *mongo.Database,
	legacyTenant string,
) error {
	if legacyTenant == "" {
		return fmt.Errorf("backfill: legacyTenant is required")
	}
	for _, coll := range []string{
		"events",
		"deliveries",
		"delivery_attempts",
	} {
		_, err := db.Collection(coll).UpdateMany(
			ctx,
			bson.M{"tenant_id": bson.M{"$exists": false}},
			bson.M{"$set": bson.M{"tenant_id": legacyTenant}},
		)
		if err != nil {
			return fmt.Errorf("backfill %s: %w", coll, err)
		}
	}
	return nil
}
