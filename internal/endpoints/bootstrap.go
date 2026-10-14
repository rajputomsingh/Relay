package endpoints

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// BootstrapConfig describes the default endpoint to create when none exist.
type BootstrapConfig struct {
	URL           string
	SigningSecret string
	MaxAttempts   int
}

// EnsureDefaultEndpoint creates a default endpoint from legacy configuration
// when the endpoint collection is empty. It is safe to call repeatedly:
// existing endpoints are never modified or overwritten.
func EnsureDefaultEndpoint(
	ctx context.Context,
	store *Store,
	cfg BootstrapConfig,
) error {
	if cfg.URL == "" {
		return nil
	}

	count, err := store.collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("count endpoints: %w", err)
	}
	if count > 0 {
		return nil
	}

	ep := Endpoint{
		Name:          "default",
		URL:           cfg.URL,
		SigningSecret: cfg.SigningSecret,
		Enabled:       true,
		MaxAttempts:   cfg.MaxAttempts,
	}

	if ep.MaxAttempts < 1 {
		ep.MaxAttempts = 8
	}

	_, err = store.Create(ctx, ep)
	if err != nil {
		if errors.Is(err, ErrNameConflict) {
			return nil
		}
		return fmt.Errorf("create default endpoint: %w", err)
	}

	return nil
}
