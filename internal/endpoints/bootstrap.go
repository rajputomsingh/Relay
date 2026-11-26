package endpoints

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type BootstrapConfig struct {
	URL           string
	SigningSecret string
	MaxAttempts   int
	RateLimitRPS  int
}

func EnsureDefaultEndpoint(
	ctx context.Context,
	store *Store,
	tenantID string,
	cfg BootstrapConfig,
) error {
	if tenantID == "" {
		return ErrMissingTenant
	}
	if cfg.URL == "" {
		return nil
	}

	count, err := store.collection.CountDocuments(ctx, bson.M{"tenant_id": tenantID})
	if err != nil {
		return fmt.Errorf("count endpoints: %w", err)
	}
	if count > 0 {
		return nil
	}

	ep := Endpoint{
		TenantID:      tenantID,
		Name:          "default",
		URL:           cfg.URL,
		SigningSecret: cfg.SigningSecret,
		Enabled:       true,
		MaxAttempts:   cfg.MaxAttempts,
		RateLimitRPS:  cfg.RateLimitRPS,
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