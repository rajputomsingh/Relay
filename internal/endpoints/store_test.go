package endpoints

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run endpoints store integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatalf("ping mongo: %v", err)
	}

	dbName := "relay_endpoints_test_" + time.Now().Format("20060102150405")
	db := client.Database(dbName)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(cleanupCtx)
		_ = client.Disconnect(cleanupCtx)
	})

	return NewStore(db.Collection("endpoints"))
}

func TestStore_TenantIsolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tenantA := "tenant_A"
	tenantB := "tenant_B"

	createdA, err := store.Create(ctx, Endpoint{
		TenantID:    tenantA,
		Name:        "Tenant A Hook",
		URL:         "https://example.com/hook",
		MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Create Tenant A endpoint: %v", err)
	}

	createdB, err := store.Create(ctx, Endpoint{
		TenantID:    tenantB,
		Name:        "Tenant B Hook",
		URL:         "https://example.com/hook",
		MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Create Tenant B endpoint: %v", err)
	}

	// Cross-tenant Get
	if _, err := store.Get(ctx, tenantA, createdB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Get: expected ErrNotFound, got %v", err)
	}

	// Own-tenant Get works
	if _, err := store.Get(ctx, tenantB, createdB.ID); err != nil {
		t.Errorf("own Get failed: %v", err)
	}

	// List scoping
	listA, err := store.List(ctx, tenantA)
	if err != nil {
		t.Fatalf("List Tenant A: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != createdA.ID {
		t.Errorf("List for Tenant A returned incorrect endpoints: %+v", listA)
	}

	// Cross-tenant Update
	updateName := "Hacked Name"
	if _, err := store.Update(ctx, tenantA, createdB.ID, Update{Name: &updateName}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Update: expected ErrNotFound, got %v", err)
	}

	checkB, _ := store.Get(ctx, tenantB, createdB.ID)
	if checkB.Name == "Hacked Name" {
		t.Errorf("cross-tenant Update succeeded! Data compromised.")
	}

	// Cross-tenant Disable
	if _, err := store.Disable(ctx, tenantA, createdB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Disable: expected ErrNotFound, got %v", err)
	}

	checkB, _ = store.Get(ctx, tenantB, createdB.ID)
	if !checkB.Enabled {
		t.Errorf("cross-tenant Disable succeeded! Data compromised.")
	}
}

func TestStore_MissingTenantContext(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Get(ctx, "", bson.NewObjectID()); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("Get: expected ErrMissingTenant, got %v", err)
	}
	if _, err := store.List(ctx, ""); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("List: expected ErrMissingTenant, got %v", err)
	}
	if _, err := store.Create(ctx, Endpoint{Name: "fail", URL: "https://example.com", MaxAttempts: 5}); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("Create: expected ErrMissingTenant, got %v", err)
	}
}