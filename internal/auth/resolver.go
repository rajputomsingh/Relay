package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"

	"github.com/rajputomsingh/relay/internal/tenant"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrInvalidKey    = errors.New("auth: invalid key")
	ErrRevokedKey    = errors.New("auth: revoked key")
	ErrUnresolvedTen = errors.New("auth: key has no tenant mapping")
)

// Resolver maps a presented credential to a trusted TenantID.
type Resolver interface {
	Resolve(ctx context.Context, presentedKey string) (tenant.TenantID, error)
}

// LegacyResolver preserves existing RELAY_API_KEY behavior with an
// explicit, deterministic tenant mapping (RFC #76).
type LegacyResolver struct {
	Key    string
	Tenant tenant.TenantID
}

func (r LegacyResolver) Resolve(_ context.Context, presented string) (tenant.TenantID, error) {
	if r.Key == "" {
		return "", ErrInvalidKey
	}
	if subtle.ConstantTimeCompare([]byte(presented), []byte(r.Key)) != 1 {
		return "", ErrInvalidKey
	}
	if r.Tenant == "" {
		return "", ErrUnresolvedTen
	}
	return r.Tenant, nil
}

// MongoResolver looks up hashed API keys in the api_keys collection.
type MongoResolver struct {
	Coll *mongo.Collection
}

func (m MongoResolver) Resolve(ctx context.Context, presented string) (tenant.TenantID, error) {
	sum := sha256.Sum256([]byte(presented))
	hash := hex.EncodeToString(sum[:])

	var doc struct {
		TenantID  string     `bson:"tenant_id"`
		RevokedAt *time.Time `bson:"revoked_at"`
	}
	err := m.Coll.FindOne(ctx, bson.M{"key_hash": hash}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", ErrInvalidKey
	}
	if err != nil {
		return "", err
	}
	if doc.RevokedAt != nil {
		return "", ErrRevokedKey
	}
	if doc.TenantID == "" {
		return "", ErrUnresolvedTen
	}
	return tenant.TenantID(doc.TenantID), nil
}

// ChainResolver tries resolvers in order; returns the first success,
// or the last error. Used so the legacy key works while Mongo keys roll out.
type ChainResolver []Resolver

func (c ChainResolver) Resolve(ctx context.Context, presented string) (tenant.TenantID, error) {
	var lastErr error = ErrInvalidKey
	for _, r := range c {
		tid, err := r.Resolve(ctx, presented)
		if err == nil {
			return tid, nil
		}
		lastErr = err
	}
	return "", lastErr
}
