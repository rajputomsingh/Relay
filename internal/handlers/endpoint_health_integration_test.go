package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func newEndpointHealthIntegration(t *testing.T) (*mongo.Database, *http.ServeMux) {
	t.Helper()

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run MongoDB endpoint health integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-endpoint-health-integration-tests").
		SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("connect to test MongoDB: %v", err)
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		disconnectCtx, disconnectCancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer disconnectCancel()

		_ = client.Disconnect(disconnectCtx)
		t.Fatalf("ping test MongoDB: %v", err)
	}

	dbName := fmt.Sprintf("rhet_%x", time.Now().UnixNano())
	db := client.Database(dbName)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer cleanupCancel()

		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop integration test database: %v", err)
		}
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test MongoDB client: %v", err)
		}
	})

	epStore := endpoints.NewStore(db.Collection("endpoints"))
	mux := http.NewServeMux()
	NewEndpointHealthHandler(
		epStore,
		db.Collection("deliveries"),
		db.Collection("delivery_attempts"),
		testAPIKey,
	).Register(mux)

	return db, mux
}

func TestEndpointHealthIntegration(t *testing.T) {
	db, mux := newEndpointHealthIntegration(t)
	ctx := context.Background()

	epStore := endpoints.NewStore(db.Collection("endpoints"))
	ep, err := epStore.Create(ctx, endpoints.Endpoint{
		Name:        "health-test",
		URL:         "https://example.com/webhook",
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	now := time.Now().UTC()
	deliveredAt := now.Add(-30 * time.Minute)
	failedOld := now.Add(-40 * time.Minute)
	failedRecent := now.Add(-10 * time.Minute)
	failedNewest := now.Add(-5 * time.Minute)

	_, err = db.Collection("deliveries").InsertMany(ctx, []any{
		delivery.Delivery{
			EventID:     bson.NewObjectID(),
			EndpointID:  ep.ID,
			Status:      delivery.StatusDelivered,
			DeliveredAt: &deliveredAt,
			CreatedAt:   deliveredAt,
			UpdatedAt:   deliveredAt,
		},
		delivery.Delivery{
			EventID:    bson.NewObjectID(),
			EndpointID: ep.ID,
			Status:     delivery.StatusFailed,
			FailedAt:   &failedOld,
			CreatedAt:  failedOld,
			UpdatedAt:  failedOld,
		},
		delivery.Delivery{
			EventID:    bson.NewObjectID(),
			EndpointID: ep.ID,
			Status:     delivery.StatusFailed,
			FailedAt:   &failedRecent,
			CreatedAt:  failedRecent,
			UpdatedAt:  failedRecent,
		},
		delivery.Delivery{
			EventID:    bson.NewObjectID(),
			EndpointID: ep.ID,
			Status:     delivery.StatusFailed,
			FailedAt:   &failedNewest,
			CreatedAt:  failedNewest,
			UpdatedAt:  failedNewest,
		},
	})
	if err != nil {
		t.Fatalf("insert deliveries: %v", err)
	}

	_, err = db.Collection("delivery_attempts").InsertMany(ctx, []any{
		delivery.Attempt{
			EventID:       bson.NewObjectID(),
			EndpointID:    ep.ID,
			AttemptNumber: 1,
			StartedAt:     now.Add(-25 * time.Minute),
			FinishedAt:    now.Add(-25 * time.Minute),
			Outcome:       attemptOutcomeDelivered,
		},
		delivery.Attempt{
			EventID:       bson.NewObjectID(),
			EndpointID:    ep.ID,
			AttemptNumber: 1,
			StartedAt:     now.Add(-20 * time.Minute),
			FinishedAt:    now.Add(-20 * time.Minute),
			Outcome:       attemptOutcomeDelivered,
		},
		delivery.Attempt{
			EventID:       bson.NewObjectID(),
			EndpointID:    ep.ID,
			AttemptNumber: 1,
			StartedAt:     now.Add(-15 * time.Minute),
			FinishedAt:    now.Add(-15 * time.Minute),
			Outcome:       attemptOutcomeDelivered,
		},
		delivery.Attempt{
			EventID:       bson.NewObjectID(),
			EndpointID:    ep.ID,
			AttemptNumber: 2,
			StartedAt:     now.Add(-5 * time.Minute),
			FinishedAt:    now.Add(-5 * time.Minute),
			Outcome:       attemptOutcomeFailed,
		},
		delivery.Attempt{
			EventID:       bson.NewObjectID(),
			EndpointID:    ep.ID,
			AttemptNumber: 1,
			StartedAt:     now.Add(-2 * time.Hour),
			FinishedAt:    now.Add(-2 * time.Hour),
			Outcome:       attemptOutcomeFailed,
		},
	})
	if err != nil {
		t.Fatalf("insert attempts: %v", err)
	}

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/"+ep.ID.Hex()+"/health", "", testAPIKey,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp endpointHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Attempts1h != 4 {
		t.Fatalf("expected 4 attempts in window, got %d", resp.Attempts1h)
	}
	if resp.Successes1h != 3 || resp.Failures1h != 1 {
		t.Fatalf("unexpected successes/failures: %+v", resp)
	}
	if resp.SuccessRate1h != 75 {
		t.Fatalf("expected success rate 75, got %v", resp.SuccessRate1h)
	}
	if resp.ConsecutiveFailures != 2 {
		t.Fatalf("expected 2 consecutive failures, got %d", resp.ConsecutiveFailures)
	}
	if resp.LastSuccessAt == nil || resp.LastSuccessAt.UnixMilli() != deliveredAt.UnixMilli() {
		t.Fatalf("unexpected last_success_at: %v", resp.LastSuccessAt)
	}
	if resp.LastFailureAt == nil || resp.LastFailureAt.UnixMilli() != failedNewest.UnixMilli() {
		t.Fatalf("unexpected last_failure_at: %v", resp.LastFailureAt)
	}
}

func TestEndpointHealthIntegrationNoHistory(t *testing.T) {
	db, mux := newEndpointHealthIntegration(t)
	ctx := context.Background()

	epStore := endpoints.NewStore(db.Collection("endpoints"))
	ep, err := epStore.Create(ctx, endpoints.Endpoint{
		Name:        "empty-health",
		URL:         "https://example.com/webhook",
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/"+ep.ID.Hex()+"/health", "", testAPIKey,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"last_success_at":null`) ||
		!strings.Contains(rec.Body.String(), `"last_failure_at":null`) {
		t.Fatalf("expected null timestamps, got %s", rec.Body.String())
	}

	var resp endpointHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Attempts1h != 0 || resp.SuccessRate1h != 0 || resp.ConsecutiveFailures != 0 {
		t.Fatalf("expected zero stats, got %+v", resp)
	}
}

func TestEndpointHealthIntegrationScenarios(t *testing.T) {
	db, mux := newEndpointHealthIntegration(t)
	ctx := context.Background()

	epStore := endpoints.NewStore(db.Collection("endpoints"))
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")
	now := time.Now().UTC()

	createEndpoint := func(name string) endpoints.Endpoint {
		t.Helper()
		ep, err := epStore.Create(ctx, endpoints.Endpoint{
			Name:        name,
			URL:         "https://example.com/webhook",
			MaxAttempts: 3,
		})
		if err != nil {
			t.Fatalf("create endpoint %q: %v", name, err)
		}
		return ep
	}

	insertAttempt := func(endpointID bson.ObjectID, at time.Time, outcome string) {
		t.Helper()
		_, err := attempts.InsertOne(ctx, delivery.Attempt{
			EventID:       bson.NewObjectID(),
			EndpointID:    endpointID,
			AttemptNumber: 1,
			StartedAt:     at,
			FinishedAt:    at,
			Outcome:       outcome,
		})
		if err != nil {
			t.Fatalf("insert attempt: %v", err)
		}
	}

	insertDelivery := func(endpointID bson.ObjectID, status string, at time.Time) {
		t.Helper()
		d := delivery.Delivery{
			EventID:    bson.NewObjectID(),
			EndpointID: endpointID,
			Status:     status,
			CreatedAt:  at,
			UpdatedAt:  at,
		}
		if status == delivery.StatusDelivered {
			d.DeliveredAt = &at
		} else {
			d.FailedAt = &at
		}
		if _, err := deliveries.InsertOne(ctx, d); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}

	getHealth := func(endpointID bson.ObjectID) endpointHealthResponse {
		t.Helper()
		rec := performEndpointRequest(
			mux,
			http.MethodGet,
			"/v1/endpoints/"+endpointID.Hex()+"/health",
			"",
			testAPIKey,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp endpointHealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp
	}

	t.Run("success only", func(t *testing.T) {
		ep := createEndpoint("success-only")
		at := now.Add(-10 * time.Minute)
		insertAttempt(ep.ID, at, attemptOutcomeDelivered)
		insertDelivery(ep.ID, delivery.StatusDelivered, at)

		resp := getHealth(ep.ID)
		if resp.Attempts1h != 1 || resp.Successes1h != 1 || resp.Failures1h != 0 {
			t.Fatalf("unexpected counts: %+v", resp)
		}
		if resp.SuccessRate1h != 100 {
			t.Fatalf("expected 100, got %v", resp.SuccessRate1h)
		}
		if resp.ConsecutiveFailures != 0 {
			t.Fatalf("expected 0 consecutive failures, got %d", resp.ConsecutiveFailures)
		}
		if resp.LastSuccessAt == nil || resp.LastSuccessAt.UnixMilli() != at.UnixMilli() {
			t.Fatalf("unexpected last_success_at: %v", resp.LastSuccessAt)
		}
		if resp.LastFailureAt != nil {
			t.Fatalf("expected nil last_failure_at, got %v", resp.LastFailureAt)
		}
	})

	t.Run("failure only", func(t *testing.T) {
		ep := createEndpoint("failure-only")
		first := now.Add(-20 * time.Minute)
		second := now.Add(-10 * time.Minute)
		insertAttempt(ep.ID, first, attemptOutcomeFailed)
		insertAttempt(ep.ID, second, attemptOutcomeFailed)
		insertDelivery(ep.ID, delivery.StatusFailed, first)
		insertDelivery(ep.ID, delivery.StatusFailed, second)

		resp := getHealth(ep.ID)
		if resp.Attempts1h != 2 || resp.Successes1h != 0 || resp.Failures1h != 2 {
			t.Fatalf("unexpected counts: %+v", resp)
		}
		if resp.SuccessRate1h != 0 {
			t.Fatalf("expected 0, got %v", resp.SuccessRate1h)
		}
		if resp.ConsecutiveFailures != 2 {
			t.Fatalf("expected 2 consecutive failures, got %d", resp.ConsecutiveFailures)
		}
		if resp.LastSuccessAt != nil {
			t.Fatalf("expected nil last_success_at, got %v", resp.LastSuccessAt)
		}
		if resp.LastFailureAt == nil || resp.LastFailureAt.UnixMilli() != second.UnixMilli() {
			t.Fatalf("unexpected last_failure_at: %v", resp.LastFailureAt)
		}
	})

	t.Run("latest timestamps outside one hour window", func(t *testing.T) {
		ep := createEndpoint("outside-window")
		successAt := now.Add(-3 * time.Hour)
		failureAt := now.Add(-2 * time.Hour)
		insertAttempt(ep.ID, successAt, attemptOutcomeDelivered)
		insertAttempt(ep.ID, failureAt, attemptOutcomeFailed)
		insertDelivery(ep.ID, delivery.StatusDelivered, successAt)
		insertDelivery(ep.ID, delivery.StatusFailed, failureAt)

		resp := getHealth(ep.ID)
		if resp.Attempts1h != 0 || resp.SuccessRate1h != 0 {
			t.Fatalf("expected zero attempts and rate, got %+v", resp)
		}
		if resp.ConsecutiveFailures != 1 {
			t.Fatalf("expected 1 consecutive failure, got %d", resp.ConsecutiveFailures)
		}
		if resp.LastSuccessAt == nil || resp.LastSuccessAt.UnixMilli() != successAt.UnixMilli() {
			t.Fatalf("unexpected last_success_at: %v", resp.LastSuccessAt)
		}
		if resp.LastFailureAt == nil || resp.LastFailureAt.UnixMilli() != failureAt.UnixMilli() {
			t.Fatalf("unexpected last_failure_at: %v", resp.LastFailureAt)
		}
	})
}
