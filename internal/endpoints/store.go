package endpoints

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrNotFound is returned when an endpoint does not exist.
var ErrNotFound = errors.New("endpoint not found")

// ErrNameConflict is returned when a name is already used by an active endpoint.
var ErrNameConflict = errors.New("endpoint name already exists")

// Store persists endpoints in MongoDB.
type Store struct {
	collection *mongo.Collection
}

// NewStore creates a Store backed by the given collection.
func NewStore(collection *mongo.Collection) *Store {
	return &Store{collection: collection}
}

// Create inserts a new endpoint.
func (s *Store) Create(
	ctx context.Context,
	ep Endpoint,
) (Endpoint, error) {
	if err := ep.Validate(); err != nil {
		return Endpoint{}, err
	}

	now := time.Now().UTC()
	ep.ID = bson.NewObjectID()
	ep.CreatedAt = now
	ep.UpdatedAt = now

	// New endpoints are enabled unless the caller explicitly disabled them.
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

// List returns all endpoints, enabled and disabled.
func (s *Store) List(ctx context.Context) ([]Endpoint, error) {
	cursor, err := s.collection.Find(
		ctx,
		bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("find endpoints: %w", err)
	}
	defer cursor.Close(ctx)

	result := make([]Endpoint, 0)
	if err := cursor.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("decode endpoints: %w", err)
	}

	return result, nil
}

// Get returns a single endpoint by ID.
func (s *Store) Get(
	ctx context.Context,
	id bson.ObjectID,
) (Endpoint, error) {
	var ep Endpoint
	err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&ep)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("find endpoint: %w", err)
	}
	return ep, nil
}

// Update applies a partial update to an endpoint.
func (s *Store) Update(
	ctx context.Context,
	id bson.ObjectID,
	update Update,
) (Endpoint, error) {
	existing, err := s.Get(ctx, id)
	if err != nil {
		return Endpoint{}, err
	}

	if err := update.Apply(&existing); err != nil {
		return Endpoint{}, err
	}
	existing.UpdatedAt = time.Now().UTC()

	result, err := s.collection.ReplaceOne(
		ctx,
		bson.M{"_id": id},
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

// Disable soft-deletes an endpoint.
func (s *Store) Disable(
	ctx context.Context,
	id bson.ObjectID,
) (Endpoint, error) {
	now := time.Now().UTC()

	var updated Endpoint
	err := s.collection.FindOneAndUpdate(
		ctx,
		bson.M{"_id": id},
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
