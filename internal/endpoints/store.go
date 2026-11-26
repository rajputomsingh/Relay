package endpoints

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrNotFound = errors.New("endpoint not found")
var ErrNameConflict = errors.New("endpoint name already exists")
var ErrMissingTenant = errors.New("tenant ID is required")

type Store struct {
	collection *mongo.Collection
}

func NewStore(collection *mongo.Collection) *Store {
	return &Store{collection: collection}
}

func (s *Store) Create(ctx context.Context, ep Endpoint) (Endpoint, error) {
	if ep.TenantID == "" {
		return Endpoint{}, ErrMissingTenant
	}
	if err := ep.Validate(); err != nil {
		return Endpoint{}, err
	}

	now := time.Now().UTC()
	ep.ID = bson.NewObjectID()
	ep.CreatedAt = now
	ep.UpdatedAt = now

	if !ep.Enabled && ep.DisabledAt == nil {
		ep.Enabled = true
	}

	_, err := s.collection.InsertOne(ctx, ep)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return Endpoint{}, ErrNameConflict
		}
		return Endpoint{}, fmt.Errorf("insert endpoint: %w", err)
	}

	return ep, nil
}

func (s *Store) List(ctx context.Context, tenantID string) ([]Endpoint, error) {
	if tenantID == "" {
		return nil, ErrMissingTenant
	}

	cursor, err := s.collection.Find(
		ctx,
		bson.M{"tenant_id": tenantID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("find endpoints: %w", err)
	}
	defer func() {
		if err := cursor.Close(ctx); err != nil {
			slog.Warn("failed to close endpoints cursor", "error", err)
		}
	}()

	result := make([]Endpoint, 0)
	if err := cursor.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("decode endpoints: %w", err)
	}

	return result, nil
}

func (s *Store) Get(ctx context.Context, tenantID string, id bson.ObjectID) (Endpoint, error) {
	if tenantID == "" {
		return Endpoint{}, ErrMissingTenant
	}

	var ep Endpoint
	err := s.collection.FindOne(ctx, bson.M{"_id": id, "tenant_id": tenantID}).Decode(&ep)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("find endpoint: %w", err)
	}
	return ep, nil
}

func (s *Store) Update(ctx context.Context, tenantID string, id bson.ObjectID, update Update) (Endpoint, error) {
	if tenantID == "" {
		return Endpoint{}, ErrMissingTenant
	}

	existing, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return Endpoint{}, err
	}

	if err := update.Apply(&existing); err != nil {
		return Endpoint{}, err
	}
	existing.UpdatedAt = time.Now().UTC()

	result, err := s.collection.ReplaceOne(
		ctx,
		bson.M{"_id": id, "tenant_id": tenantID},
		existing,
	)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return Endpoint{}, ErrNameConflict
		}
		return Endpoint{}, fmt.Errorf("update endpoint: %w", err)
	}
	if result.MatchedCount == 0 {
		return Endpoint{}, ErrNotFound
	}

	return existing, nil
}

func (s *Store) Disable(ctx context.Context, tenantID string, id bson.ObjectID) (Endpoint, error) {
	if tenantID == "" {
		return Endpoint{}, ErrMissingTenant
	}

	now := time.Now().UTC()

	var updated Endpoint
	err := s.collection.FindOneAndUpdate(
		ctx,
		bson.M{"_id": id, "tenant_id": tenantID},
		bson.M{
			"$set": bson.M{
				"enabled":     false,
				"disabled_at": now,
				"updated_at":  now,
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("disable endpoint: %w", err)
	}

	return updated, nil
}