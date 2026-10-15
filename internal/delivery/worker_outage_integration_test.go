package delivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
}

func requireContainer(t *testing.T, name string) {
	t.Helper()

	cmd := exec.Command(
		"docker", "container", "inspect", name,
		"--format", "{{.State.Status}}",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("docker container %q not found: %v", name, err)
	}

	status := strings.TrimSpace(string(out))
	if status != "running" {
		t.Skipf(
			"docker container %q is not running (status %q)",
			name,
			status,
		)
	}
}

func dockerStop(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stop", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker stop %s: %v; %s", name, err, string(out))
	}
}

func dockerStart(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "start", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker start %s: %v; %s", name, err, string(out))
	}
}

func waitForMongo(
	t *testing.T,
	client *mongo.Client,
	reachable bool,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(
			context.Background(), 2*time.Second,
		)
		err := client.Ping(ctx, readpref.Primary())
		cancel()

		if reachable && err == nil {
			return
		}
		if !reachable && err != nil {
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf(
		"MongoDB reachability did not match expectation (reachable=%v): %v",
		reachable,
		lastErr,
	)
}

// waitForCondition polls predicate until it returns true or times out.
func waitForCondition(
	t *testing.T,
	description string,
	timeout time.Duration,
	predicate func() bool,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition %q not met within %s", description, timeout)
}

func TestWorkerRecoversFromMongoOutage(t *testing.T) {
	requireDocker(t)

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run worker outage tests")
	}

	container := strings.TrimSpace(os.Getenv("MONGODB_TEST_CONTAINER"))
	if container == "" {
		container = "relay-mongo"
	}
	requireContainer(t, container)

	assertClient, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-worker-outage-assert").
		SetServerSelectionTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("connect assert client: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()
		_ = assertClient.Disconnect(ctx)
	}()

	db := assertClient.Database("relay_worker_outage_test")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()
		_ = db.Drop(ctx)
	})

	events := db.Collection("events")
	endpoints := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")

	// -------------------------------------------------------------
	// Step 1 — seed event, endpoint, delivery.
	// -------------------------------------------------------------

	waitForMongo(t, assertClient, true, 10*time.Second)

	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := bson.NewObjectID()
	endpointID := bson.NewObjectID()
	deliveryID := bson.NewObjectID()
	now := time.Now().UTC()

	seedCtx, cancelSeed := context.WithTimeout(
		context.Background(), 5*time.Second,
	)
	defer cancelSeed()

	if _, err := events.InsertOne(seedCtx, bson.M{
		"_id":             eventID,
		"source":          "outage-test",
		"event_type":      "reliability.test",
		"data":            bson.M{"k": "v"},
		"idempotency_key": "outage-" + eventID.Hex(),
		"received_at":     now,
		"status":          "accepted",
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	if _, err := endpoints.InsertOne(seedCtx, bson.M{
		"_id":          endpointID,
		"name":         "outage-test-" + endpointID.Hex(),
		"url":          server.URL,
		"enabled":      true,
		"max_attempts": 3,
		"created_at":   now,
		"updated_at":   now,
	}); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	if _, err := deliveries.InsertOne(seedCtx, bson.M{
		"_id":          deliveryID,
		"event_id":     eventID,
		"endpoint_id":  endpointID,
		"status":       StatusPending,
		"attempts":     0,
		"max_attempts": 3,
		"created_at":   now,
		"updated_at":   now,
	}); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	// -------------------------------------------------------------
	// Step 2 — take MongoDB down BEFORE the worker starts.
	// -------------------------------------------------------------

	dockerStop(t, container)
	defer dockerStart(t, container)

	waitForMongo(t, assertClient, false, 15*time.Second)

	// -------------------------------------------------------------
	// Step 3 — start the worker against the downed MongoDB.
	// -------------------------------------------------------------

	worker := NewWorker(
		events,
		endpoints,
		deliveries,
		attempts,
		Config{
			PollInterval:   200 * time.Millisecond,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  10 * time.Second,
			Concurrency:    1,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	// Wait for the worker to log at least one processing error.
	// Each failed poll takes up to ServerSelectionTimeout (2s) to
	// return, so allow a generous margin.
	waitForCondition(
		t,
		"worker recorded a processing error during outage",
		15*time.Second,
		func() bool {
			return worker.ProcessingErrorsTotal() > 0
		},
	)

	// Worker must still be running.
	select {
	case <-done:
		t.Fatal("worker exited during MongoDB outage")
	default:
	}

	// -------------------------------------------------------------
	// Step 4 — restart MongoDB and wait for the recovery.
	// -------------------------------------------------------------

	dockerStart(t, container)
	waitForMongo(t, assertClient, true, 30*time.Second)

	select {
	case <-received:
	case <-time.After(30 * time.Second):
		t.Fatal("webhook was not delivered after recovery")
	}

	// Wait for the delivery to reach a terminal state. The webhook
	// firing and the DB write that follows it are separate; the DB
	// write commits slightly after the HTTP response.
	waitForCondition(
		t,
		"delivery marked delivered",
		10*time.Second,
		func() bool {
			readCtx, readCancel := context.WithTimeout(
				context.Background(), 2*time.Second,
			)
			defer readCancel()

			var d Delivery
			if err := deliveries.FindOne(
				readCtx, bson.M{"_id": deliveryID},
			).Decode(&d); err != nil {
				return false
			}
			return d.Status == StatusDelivered
		},
	)

	cancel()
	<-done
}
