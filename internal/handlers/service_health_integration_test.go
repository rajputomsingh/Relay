package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/lifecycle"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const serviceHealthIntegrationPollInterval = 50 * time.Millisecond

// newServiceHealthIntegrationClient connects to the MongoDB named by
// MONGODB_TEST_URI and returns the client with a throwaway database.
func newServiceHealthIntegrationClient(t *testing.T) (*mongo.Client, *mongo.Database) {
	t.Helper()

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run service health integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-service-health-integration-tests").
		SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("connect to test MongoDB: %v", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		t.Fatalf("ping test MongoDB: %v", err)
	}

	db := client.Database(fmt.Sprintf("rsh_%x", time.Now().UnixNano()))

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop integration test database: %v", err)
		}
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test MongoDB client: %v", err)
		}
	})

	return client, db
}

func newServiceHealthIntegrationWorker(db *mongo.Database) *delivery.Worker {
	events := db.Collection("events")
	deliveries := db.Collection("deliveries")

	return delivery.NewWorker(
		events,
		db.Collection("endpoints"),
		deliveries,
		db.Collection("delivery_attempts"),
		lifecycle.NewAggregator(events, deliveries),
		delivery.Config{
			PollInterval:   serviceHealthIntegrationPollInterval,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    1,
		},
	)
}

func newServiceHealthIntegrationMux(client pinger, worker *delivery.Worker) *http.ServeMux {
	mux := http.NewServeMux()
	NewHealthHandler(client).
		WithWorker(worker, serviceHealthIntegrationPollInterval).
		Register(mux)
	return mux
}

// The worker runs against an empty queue, so every poll finds nothing to
// deliver. The service must still report the worker as ok: liveness comes
// from the last poll, not the last delivery.
func TestServiceHealthIntegrationIdleWorkerIsOK(t *testing.T) {
	client, db := newServiceHealthIntegrationClient(t)
	worker := newServiceHealthIntegrationWorker(db)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	deadline := time.Now().Add(10 * time.Second)
	for worker.LastPolledAt().IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("worker never recorded a poll on an empty queue")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mux := newServiceHealthIntegrationMux(client, worker)
	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	body := decodeServiceHealthBody(t, recorder)
	for field, want := range map[string]string{
		"status": "ok",
		"mongo":  "ok",
		"worker": "ok",
	} {
		if body[field] != want {
			t.Errorf("%s = %v, want %q", field, body[field], want)
		}
	}

	if got := worker.LastProcessedAt(); !got.IsZero() {
		t.Errorf("LastProcessedAt = %v, want zero: the queue was empty", got)
	}
}

func TestServiceHealthIntegrationWorkerNotStartedIsDisabled(t *testing.T) {
	client, db := newServiceHealthIntegrationClient(t)
	worker := newServiceHealthIntegrationWorker(db)

	mux := newServiceHealthIntegrationMux(client, worker)
	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	body := decodeServiceHealthBody(t, recorder)
	if body["worker"] != "disabled" {
		t.Errorf("worker = %v, want disabled", body["worker"])
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
}

func TestServiceHealthIntegrationMongoUnreachableIsDown(t *testing.T) {
	// Nothing listens on port 1, so every ping fails.
	client, err := mongo.Connect(options.Client().
		ApplyURI("mongodb://127.0.0.1:1").
		SetServerSelectionTimeout(500 * time.Millisecond))
	if err != nil {
		t.Fatalf("create unreachable MongoDB client: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(context.Background())
	})

	mux := http.NewServeMux()
	NewHealthHandler(client).Register(mux)

	recorder := performHealthRequest(mux, http.MethodGet, "/v1/health")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}

	body := decodeServiceHealthBody(t, recorder)
	if body["status"] != "down" {
		t.Errorf("status = %v, want down", body["status"])
	}
	if body["mongo"] != "unreachable" {
		t.Errorf("mongo = %v, want unreachable", body["mongo"])
	}
	if _, ok := body["worker"]; ok {
		t.Errorf("worker = %v, want it omitted when MongoDB is down", body["worker"])
	}
}
