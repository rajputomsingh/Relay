package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/security"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type workerIntegrationDB struct {
	events     *mongo.Collection
	endpoints  *mongo.Collection
	deliveries *mongo.Collection
	attempts   *mongo.Collection
}

func newWorkerIntegrationDB(t *testing.T) workerIntegrationDB {
	t.Helper()

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run delivery worker integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-delivery-integration-tests").
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

	dbName := fmt.Sprintf("rdt_%x", time.Now().UnixNano())
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

	return workerIntegrationDB{
		events:     db.Collection("events"),
		endpoints:  db.Collection("endpoints"),
		deliveries: db.Collection("deliveries"),
		attempts:   db.Collection("delivery_attempts"),
	}
}

// insertWorkerIntegrationEvent inserts a minimal event document.
func insertWorkerIntegrationEvent(
	t *testing.T,
	collection *mongo.Collection,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	now := time.Now().UTC()
	event := bson.M{
		"_id":             id,
		"source":          "integration-test",
		"event_type":      "invoice.paid",
		"data":            bson.M{"invoice_id": "inv-worker-test"},
		"idempotency_key": "worker-test-" + id.Hex(),
		"received_at":     now,
		"status":          "accepted",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := collection.InsertOne(ctx, event); err != nil {
		t.Fatalf("insert integration event: %v", err)
	}

	return id
}

// insertWorkerIntegrationEndpoint inserts an enabled endpoint.
func insertWorkerIntegrationEndpoint(
	t *testing.T,
	collection *mongo.Collection,
	url string,
	secret string,
	maxAttempts int,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	now := time.Now().UTC()

	ep := bson.M{
		"_id":          id,
		"name":         "test-" + id.Hex(),
		"url":          url,
		"enabled":      true,
		"max_attempts": maxAttempts,
		"created_at":   now,
		"updated_at":   now,
	}
	if secret != "" {
		ep["signing_secret"] = secret
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := collection.InsertOne(ctx, ep); err != nil {
		t.Fatalf("insert integration endpoint: %v", err)
	}

	return id
}

// insertWorkerIntegrationEndpointWithRateLimit inserts an enabled
// endpoint with the given rate limit.
func insertWorkerIntegrationEndpointWithRateLimit(
	t *testing.T,
	collection *mongo.Collection,
	url string,
	secret string,
	maxAttempts int,
	rateLimitRPS int,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	now := time.Now().UTC()

	ep := bson.M{
		"_id":            id,
		"name":           "test-" + id.Hex(),
		"url":            url,
		"enabled":        true,
		"max_attempts":   maxAttempts,
		"rate_limit_rps": rateLimitRPS,
		"created_at":     now,
		"updated_at":     now,
	}
	if secret != "" {
		ep["signing_secret"] = secret
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := collection.InsertOne(ctx, ep); err != nil {
		t.Fatalf("insert integration endpoint: %v", err)
	}

	return id
}

// insertWorkerIntegrationDelivery inserts a due delivery.
func insertWorkerIntegrationDelivery(
	t *testing.T,
	collection *mongo.Collection,
	eventID bson.ObjectID,
	endpointID bson.ObjectID,
	maxAttempts int,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	now := time.Now().UTC()

	doc := bson.M{
		"_id":          id,
		"event_id":     eventID,
		"endpoint_id":  endpointID,
		"status":       StatusPending,
		"attempts":     0,
		"max_attempts": maxAttempts,
		"created_at":   now,
		"updated_at":   now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := collection.InsertOne(ctx, doc); err != nil {
		t.Fatalf("insert integration delivery: %v", err)
	}

	return id
}

// readWorkerDeliveryState reads the current delivery document.
func readWorkerDeliveryState(
	t *testing.T,
	collection *mongo.Collection,
	id bson.ObjectID,
) Delivery {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var d Delivery
	if err := collection.FindOne(
		ctx, bson.M{"_id": id},
	).Decode(&d); err != nil {
		t.Fatalf("read delivery state: %v", err)
	}
	return d
}

// readWorkerAttempts returns all persisted attempts for a delivery's
// event/endpoint pair.
func readWorkerAttempts(
	t *testing.T,
	collection *mongo.Collection,
	eventID, endpointID bson.ObjectID,
) []Attempt {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := collection.Find(
		ctx,
		bson.M{
			"event_id":    eventID,
			"endpoint_id": endpointID,
		},
	)
	if err != nil {
		t.Fatalf("find delivery attempts: %v", err)
	}
	defer cursor.Close(ctx)

	var attempts []Attempt
	if err := cursor.All(ctx, &attempts); err != nil {
		t.Fatalf("decode delivery attempts: %v", err)
	}
	return attempts
}

// newIntegrationWorker builds a worker against the test DB.
func newIntegrationWorker(
	db workerIntegrationDB,
	pollInterval time.Duration,
) *Worker {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	return NewWorker(
		db.events,
		db.endpoints,
		db.deliveries,
		db.attempts,
		Config{
			PollInterval:   pollInterval,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    1,
		},
	)
}

// ---------------------------------------------------------------
// Successful delivery
// ---------------------------------------------------------------

func TestWorkerIntegrationSuccessfulDeliveryAndAttemptHistory(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		requestCount++

		if r.Method != http.MethodPost {
			t.Errorf("request method = %q; want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q; want application/json", got)
		}
		if got := r.Header.Get("X-Relay-Event-ID"); got == "" {
			t.Error("missing X-Relay-Event-ID header")
		}

		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		}
		if payload.EventType != "invoice.paid" {
			t.Errorf(
				"payload event type = %q; want invoice.paid",
				payload.EventType,
			)
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process delivery: %v", err)
	}

	if requestCount != 1 {
		t.Fatalf("webhook request count = %d; want 1", requestCount)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusDelivered {
		t.Errorf("status = %q; want delivered", state.Status)
	}
	if state.DeliveredAt == nil || state.DeliveredAt.IsZero() {
		t.Error("expected delivered_at to be set")
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID, endpointID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1", len(attempts))
	}
	if attempts[0].HTTPStatus != http.StatusNoContent {
		t.Errorf(
			"HTTP status = %d; want %d",
			attempts[0].HTTPStatus, http.StatusNoContent,
		)
	}
	if attempts[0].Outcome != "delivered" {
		t.Errorf("outcome = %q; want delivered", attempts[0].Outcome)
	}
	if attempts[0].EndpointID != endpointID {
		t.Errorf(
			"endpoint_id = %s; want %s",
			attempts[0].EndpointID.Hex(), endpointID.Hex(),
		)
	}
}

// ---------------------------------------------------------------
// Retry scheduling and exhaustion
// ---------------------------------------------------------------

func TestWorkerIntegrationTemporaryFailureSchedulesRetry(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process delivery: %v", err)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusPending {
		t.Errorf("status = %q; want pending", state.Status)
	}
	if state.Attempts != 1 {
		t.Errorf("attempts = %d; want 1", state.Attempts)
	}
	if state.NextAttemptAt == nil {
		t.Fatal("expected next_attempt_at to be set")
	}
	if !state.NextAttemptAt.After(time.Now().UTC()) {
		t.Errorf(
			"next_attempt_at = %v; want a future time",
			*state.NextAttemptAt,
		)
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if !strings.Contains(state.LastError, "503") {
		t.Errorf("last_error = %q; want HTTP 503", state.LastError)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID, endpointID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1", len(attempts))
	}
	if attempts[0].HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf(
			"HTTP status = %d; want %d",
			attempts[0].HTTPStatus, http.StatusServiceUnavailable,
		)
	}
	if attempts[0].Outcome != "failed" {
		t.Errorf("outcome = %q; want failed", attempts[0].Outcome)
	}
}

func TestWorkerIntegrationFailsAfterMaximumAttempts(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 1,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 1,
	)

	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process delivery: %v", err)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusFailed {
		t.Errorf("status = %q; want failed", state.Status)
	}
	if state.Attempts != 1 {
		t.Errorf("attempts = %d; want 1", state.Attempts)
	}
	if state.FailedAt == nil || state.FailedAt.IsZero() {
		t.Error("expected failed_at to be set")
	}
	if state.NextAttemptAt != nil {
		t.Errorf("next_attempt_at = %v; want unset", state.NextAttemptAt)
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if !strings.Contains(state.LastError, "500") {
		t.Errorf("last_error = %q; want HTTP 500", state.LastError)
	}
}

// ---------------------------------------------------------------
// Expired leases
// ---------------------------------------------------------------

func TestWorkerIntegrationReclaimsExpiredLease(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	const oldLeaseToken = "stale-lease-token"

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, "http://placeholder", "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	// Mark the delivery as processing with an expired lease.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.deliveries.UpdateOne(
		ctx,
		bson.M{"_id": deliveryID},
		bson.M{
			"$set": bson.M{
				"status":      StatusProcessing,
				"attempts":    1,
				"lease_until": time.Now().UTC().Add(-time.Minute),
				"lease_token": oldLeaseToken,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare expired lease: %v", err)
	}

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		requestCount++

		// The delivery should already be claimed with a fresh lease
		// token by the time the webhook runs.
		requestCtx, requestCancel := context.WithTimeout(
			r.Context(), 5*time.Second,
		)
		defer requestCancel()

		var d bson.M
		if err := db.deliveries.FindOne(
			requestCtx, bson.M{"_id": deliveryID},
		).Decode(&d); err != nil {
			t.Errorf("read delivery during webhook: %v", err)
			http.Error(w, "test setup failed", http.StatusInternalServerError)
			return
		}

		newToken, ok := d["lease_token"].(string)
		if !ok || newToken == "" {
			t.Error("expected a non-empty lease token during delivery")
		}
		if newToken == oldLeaseToken {
			t.Error("reclaimed lease should receive a new token")
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Point the endpoint at our server.
	_, err = db.endpoints.UpdateOne(
		ctx,
		bson.M{"_id": endpointID},
		bson.M{"$set": bson.M{"url": server.URL}},
	)
	if err != nil {
		t.Fatalf("point endpoint at server: %v", err)
	}

	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process expired lease: %v", err)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusDelivered {
		t.Errorf("status = %q; want delivered", state.Status)
	}
	if state.Attempts != 1 {
		t.Errorf("attempts = %d; want 1", state.Attempts)
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if requestCount != 1 {
		t.Errorf("webhook requests = %d; want 1", requestCount)
	}
}

func TestWorkerIntegrationDoesNotReclaimActiveLease(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	leaseUntil := time.Now().UTC().Add(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.deliveries.UpdateOne(
		ctx,
		bson.M{"_id": deliveryID},
		bson.M{
			"$set": bson.M{
				"status":      StatusProcessing,
				"attempts":    1,
				"lease_until": leaseUntil,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare active lease: %v", err)
	}

	worker := newIntegrationWorker(db, time.Second)

	err = worker.processOne(context.Background())
	if !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf(
			"process active lease error = %v; want mongo.ErrNoDocuments",
			err,
		)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusProcessing {
		t.Errorf("status = %q; want processing", state.Status)
	}
	if requestCount != 0 {
		t.Errorf("webhook requests = %d; want 0", requestCount)
	}
}

func TestWorkerIntegrationFailsExpiredExhaustedLease(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.deliveries.UpdateOne(
		ctx,
		bson.M{"_id": deliveryID},
		bson.M{
			"$set": bson.M{
				"status":      StatusProcessing,
				"attempts":    3,
				"lease_until": time.Now().UTC().Add(-time.Minute),
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare exhausted lease: %v", err)
	}

	worker := newIntegrationWorker(db, time.Second)

	err = worker.processOne(context.Background())
	if !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf(
			"process exhausted lease error = %v; want mongo.ErrNoDocuments",
			err,
		)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusFailed {
		t.Errorf("status = %q; want failed", state.Status)
	}
	if state.Attempts != 3 {
		t.Errorf("attempts = %d; want 3", state.Attempts)
	}
	if state.FailedAt == nil || state.FailedAt.IsZero() {
		t.Error("expected failed_at to be set")
	}
	if requestCount != 0 {
		t.Errorf("webhook requests = %d; want 0", requestCount)
	}
}

// ---------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------

func TestWorkerIntegrationPreventsDuplicateConcurrentClaims(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, "http://127.0.0.1:1", "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	const workerCount = 16
	start := make(chan struct{})
	results := make(chan error, workerCount)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			worker := newIntegrationWorker(db, time.Second)
			<-start

			ctx, cancel := context.WithTimeout(
				context.Background(), 5*time.Second,
			)
			defer cancel()

			claim, err := worker.claim(ctx)
			if err == nil {
				if claim.Delivery.ID != deliveryID {
					results <- fmt.Errorf(
						"claimed delivery ID = %s; want %s",
						claim.Delivery.ID.Hex(),
						deliveryID.Hex(),
					)
					return
				}
				results <- nil
				return
			}
			results <- err
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	successes := 0
	noDocuments := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, mongo.ErrNoDocuments):
			noDocuments++
		default:
			t.Errorf("unexpected claim error: %v", err)
		}
	}

	if successes != 1 {
		t.Errorf("successful claims = %d; want exactly 1", successes)
	}
	if noDocuments != workerCount-1 {
		t.Errorf(
			"unsuccessful claims = %d; want %d",
			noDocuments, workerCount-1,
		)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusProcessing {
		t.Errorf("status = %q; want processing", state.Status)
	}
	if state.LeaseUntil == nil || !state.LeaseUntil.After(time.Now()) {
		t.Errorf("expected a future lease; got %v", state.LeaseUntil)
	}
}

// ---------------------------------------------------------------
// Cancellation
// ---------------------------------------------------------------

func TestWorkerIntegrationCancellationPreservesRecoverableDelivery(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseHandler) })
	}
	defer release()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		close(requestStarted)
		<-releaseHandler
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = worker.processOne(ctx)
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not start the webhook request")
	}

	cancel()

	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after cancellation")
	}

	release()

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status == StatusDelivered {
		t.Fatal("delivery marked delivered despite no acknowledgement")
	}
	if state.Status != StatusProcessing {
		t.Fatalf(
			"status = %q; want processing until lease recovery",
			state.Status,
		)
	}
	if state.LeaseUntil == nil {
		t.Fatal("expected the interrupted delivery to retain its lease")
	}
}

// ---------------------------------------------------------------
// Stale lease token
// ---------------------------------------------------------------

func TestWorkerIntegrationStaleLeaseTokenCannotOverwrite(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		requestCount++

		// Simulate another worker reclaiming the delivery during the
		// webhook request.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		_, err := db.deliveries.UpdateOne(
			ctx,
			bson.M{"_id": bson.M{"$exists": true}},
			bson.M{"$set": bson.M{"lease_token": "newer-token"}},
		)
		if err != nil {
			t.Errorf("invalidate lease token: %v", err)
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)

	err := worker.processOne(context.Background())
	if err == nil {
		t.Fatal("expected an error when lease token was replaced")
	}
	if !strings.Contains(err.Error(), "no longer owned by this lease") {
		t.Fatalf(
			"error = %q; want no longer owned by this lease",
			err,
		)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusProcessing {
		t.Errorf("status = %q; want processing", state.Status)
	}
}

// ---------------------------------------------------------------
// Signing
// ---------------------------------------------------------------

func TestWorkerIntegrationSignsOutboundRequest(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	const secret = "0123456789abcdef0123456789abcdef"

	receivedBody := make(chan []byte, 1)
	receivedHeaders := make(chan http.Header, 1)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read webhook body: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}

		receivedBody <- body
		receivedHeaders <- r.Header.Clone()

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, secret, 3,
	)
	insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)

	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process signed event: %v", err)
	}

	var body []byte
	var headers http.Header

	select {
	case body = <-receivedBody:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook was not delivered")
	}

	select {
	case headers = <-receivedHeaders:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook headers were not observed")
	}

	ts := headers.Get(security.TimestampHeader)
	sig := headers.Get(security.SignatureHeader)

	if ts == "" {
		t.Fatalf("missing %s header", security.TimestampHeader)
	}
	if sig == "" {
		t.Fatalf("missing %s header", security.SignatureHeader)
	}

	timestamp, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		t.Fatalf("invalid timestamp %q: %v", ts, err)
	}

	signer, err := security.NewSigner(secret)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	if err := signer.Verify(timestamp, body, sig); err != nil {
		t.Fatalf("receiver-side verification failed: %v", err)
	}
}

func TestWorkerIntegrationUnsignedWhenNoSecret(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	receivedHeaders := make(chan http.Header, 1)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = io.ReadAll(r.Body)
		receivedHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process unsigned event: %v", err)
	}

	select {
	case headers := <-receivedHeaders:
		if headers.Get(security.SignatureHeader) != "" {
			t.Errorf(
				"expected no %s header for unsigned endpoint",
				security.SignatureHeader,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("webhook was not delivered")
	}
}

// ---------------------------------------------------------------
// Missing resources
// ---------------------------------------------------------------

func TestWorkerIntegrationMissingEventFailsDelivery(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Create a delivery referencing a nonexistent event.
	missingEventID := bson.NewObjectID()
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, missingEventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)

	err := worker.processOne(context.Background())
	if !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf(
			"process missing-event delivery error = %v; want mongo.ErrNoDocuments",
			err,
		)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusFailed {
		t.Errorf("status = %q; want failed", state.Status)
	}
	if !strings.Contains(state.LastError, "event not found") {
		t.Errorf("last_error = %q; want event not found", state.LastError)
	}
}

func TestWorkerIntegrationDisabledEndpointFailsDelivery(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, "http://127.0.0.1:1", "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	// Disable the endpoint.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.endpoints.UpdateOne(
		ctx,
		bson.M{"_id": endpointID},
		bson.M{"$set": bson.M{"enabled": false}},
	)
	if err != nil {
		t.Fatalf("disable endpoint: %v", err)
	}

	worker := newIntegrationWorker(db, time.Second)

	err = worker.processOne(context.Background())
	if !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf(
			"process disabled-endpoint delivery error = %v; want mongo.ErrNoDocuments",
			err,
		)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusFailed {
		t.Errorf("status = %q; want failed", state.Status)
	}
	if !strings.Contains(state.LastError, "endpoint disabled") {
		t.Errorf(
			"last_error = %q; want endpoint disabled",
			state.LastError,
		)
	}
}

// ---------------------------------------------------------------
// Worker lifecycle
// ---------------------------------------------------------------

func TestWorkerIntegrationRunStopsOnCancellation(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	worker := NewWorker(
		db.events,
		db.endpoints,
		db.deliveries,
		db.attempts,
		Config{
			PollInterval:   10 * time.Millisecond,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    1,
			Logger:         logger,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(logs.String(), "delivery worker started") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("worker did not start before the deadline")
		case <-done:
			t.Fatal("worker exited before startup was observed")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker Run did not stop after context cancellation")
	}

	if !strings.Contains(logs.String(), "delivery worker stopped") {
		t.Fatal("expected worker shutdown log")
	}
}

// ---------------------------------------------------------------
// Bounded concurrency
// ---------------------------------------------------------------

func TestWorkerIntegrationConcurrencyIsBounded(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	const (
		workers         = 4
		deliveriesTotal = 12
		handlerDelay    = 200 * time.Millisecond
	)

	var (
		current atomic.Int64
		peak    atomic.Int64
	)

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		now := current.Add(1)
		defer current.Add(-1)

		for {
			observed := peak.Load()
			if now <= observed {
				break
			}
			if peak.CompareAndSwap(observed, now) {
				break
			}
		}

		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)

	for i := 0; i < deliveriesTotal; i++ {
		eventID := insertWorkerIntegrationEvent(t, db.events)
		insertWorkerIntegrationDelivery(
			t, db.deliveries, eventID, endpointID, 3,
		)
	}

	worker := NewWorker(
		db.events,
		db.endpoints,
		db.deliveries,
		db.attempts,
		Config{
			PollInterval:   10 * time.Millisecond,
			RequestTimeout: 5 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    workers,
		},
	)

	ctx, cancel := context.WithTimeout(
		context.Background(), 15*time.Second,
	)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	deadline := time.After(15 * time.Second)
	for {
		delivered, err := db.deliveries.CountDocuments(
			context.Background(),
			bson.M{"status": StatusDelivered},
		)
		if err != nil {
			t.Fatalf("count delivered: %v", err)
		}
		if delivered == int64(deliveriesTotal) {
			break
		}

		select {
		case <-deadline:
			t.Fatalf(
				"only %d/%d deliveries reached delivered state",
				delivered, deliveriesTotal,
			)
		case <-time.After(50 * time.Millisecond):
		}
	}

	cancel()
	<-done

	observedPeak := peak.Load()
	if observedPeak > int64(workers) {
		t.Errorf(
			"observed %d concurrent HTTP requests; "+
				"concurrency limit is %d",
			observedPeak, workers,
		)
	}
}

func TestWorkerIntegrationInFlightTracksProcessing(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseHandler) })
	}
	defer release()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		close(requestStarted)
		<-releaseHandler
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, server.URL, "", 3,
	)
	insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := NewWorker(
		db.events,
		db.endpoints,
		db.deliveries,
		db.attempts,
		Config{
			PollInterval:   50 * time.Millisecond,
			RequestTimeout: 5 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    1,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(ctx)
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not start the webhook request")
	}

	deadline := time.After(2 * time.Second)
	for {
		if worker.InFlightDeliveries() == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf(
				"in-flight deliveries = %d; want 1 during request",
				worker.InFlightDeliveries(),
			)
		case <-time.After(10 * time.Millisecond):
		}
	}

	release()

	deadline = time.After(2 * time.Second)
	for {
		if worker.InFlightDeliveries() == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf(
				"in-flight deliveries = %d; want 0 after release",
				worker.InFlightDeliveries(),
			)
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-workerDone
}

// ---------------------------------------------------------------
// Rate limit release
// ---------------------------------------------------------------

func TestWorkerIntegrationReleaseClaimFencedByLeaseToken(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, "http://127.0.0.1:1", "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)

	stale := Delivery{
		ID:         deliveryID,
		LeaseToken: "stale-token",
	}

	if err := worker.releaseClaim(
		context.Background(), stale,
	); err != nil {
		t.Fatalf("release stale claim: %v", err)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusPending {
		t.Errorf(
			"status = %q; want pending (unchanged)",
			state.Status,
		)
	}
	if state.Attempts != 0 {
		t.Errorf(
			"attempts = %d; want 0 (unchanged)",
			state.Attempts,
		)
	}
}

func TestWorkerIntegrationReleaseClaimDoesNotConsumeAttempt(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(
		t, db.endpoints, "http://127.0.0.1:1", "", 3,
	)
	deliveryID := insertWorkerIntegrationDelivery(
		t, db.deliveries, eventID, endpointID, 3,
	)

	worker := newIntegrationWorker(db, time.Second)

	ctx, cancel := context.WithTimeout(
		context.Background(), 5*time.Second,
	)
	defer cancel()

	claim, err := worker.claim(ctx)
	if err != nil {
		t.Fatalf("claim delivery: %v", err)
	}

	if err := worker.releaseClaim(
		context.Background(), claim.Delivery,
	); err != nil {
		t.Fatalf("release claim: %v", err)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusPending {
		t.Errorf("status = %q; want pending", state.Status)
	}
	if state.Attempts != 0 {
		t.Errorf(
			"attempts = %d; want 0 (release must not consume an attempt)",
			state.Attempts,
		)
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if state.NextAttemptAt != nil {
		t.Errorf(
			"next_attempt_at = %v; want unset (release is not a failure)",
			state.NextAttemptAt,
		)
	}
	if state.LastError != "" {
		t.Errorf("last_error = %q; want empty", state.LastError)
	}
}

// ---------------------------------------------------------------
// End-to-end rate limiting
// ---------------------------------------------------------------

func TestWorkerIntegrationRateLimitEnforced(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	const (
		deliveriesTotal = 10
		rateLimitRPS    = 2
		concurrency     = 4
		testDuration    = 3 * time.Second
	)

	var requestCount atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	endpointID := insertWorkerIntegrationEndpointWithRateLimit(
		t, db.endpoints, server.URL, "", 3, rateLimitRPS,
	)

	for i := 0; i < deliveriesTotal; i++ {
		eventID := insertWorkerIntegrationEvent(t, db.events)
		insertWorkerIntegrationDelivery(
			t, db.deliveries, eventID, endpointID, 3,
		)
	}

	worker := NewWorker(
		db.events,
		db.endpoints,
		db.deliveries,
		db.attempts,
		Config{
			PollInterval:   50 * time.Millisecond,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    concurrency,
		},
	)

	ctx, cancel := context.WithTimeout(
		context.Background(), testDuration,
	)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	<-done

	observed := requestCount.Load()

	// At rate_limit_rps=2 for 3 seconds, a full burst plus the running
	// refill allows roughly 2 + 3*2 = 8 requests. Allow generous margin
	// for scheduler jitter.
	maxExpected := int64(rateLimitRPS * 5)
	if observed > maxExpected {
		t.Errorf(
			"observed %d requests in %s at %d rps; want at most %d",
			observed,
			testDuration,
			rateLimitRPS,
			maxExpected,
		)
	}

	// The limit must have throttled *something*.
	if observed >= deliveriesTotal {
		t.Errorf(
			"all %d deliveries completed within %s at %d rps; "+
				"rate limit was not enforced",
			deliveriesTotal,
			testDuration,
			rateLimitRPS,
		)
	}
}

func TestWorkerIntegrationRateLimitIndependentEndpoints(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	const (
		limitedTotal   = 6
		unlimitedTotal = 6
		rateLimitRPS   = 1
		concurrency    = 4
		waitFor        = 5 * time.Second
	)

	var (
		limitedCount   atomic.Int64
		unlimitedCount atomic.Int64
	)

	limitedServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		limitedCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer limitedServer.Close()

	unlimitedServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		unlimitedCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer unlimitedServer.Close()

	limitedEndpointID := insertWorkerIntegrationEndpointWithRateLimit(
		t, db.endpoints, limitedServer.URL, "", 3, rateLimitRPS,
	)
	unlimitedEndpointID := insertWorkerIntegrationEndpointWithRateLimit(
		t, db.endpoints, unlimitedServer.URL, "", 3, 0,
	)

	for i := 0; i < limitedTotal; i++ {
		eventID := insertWorkerIntegrationEvent(t, db.events)
		insertWorkerIntegrationDelivery(
			t, db.deliveries, eventID, limitedEndpointID, 3,
		)
	}
	for i := 0; i < unlimitedTotal; i++ {
		eventID := insertWorkerIntegrationEvent(t, db.events)
		insertWorkerIntegrationDelivery(
			t, db.deliveries, eventID, unlimitedEndpointID, 3,
		)
	}

	worker := NewWorker(
		db.events,
		db.endpoints,
		db.deliveries,
		db.attempts,
		Config{
			PollInterval:   50 * time.Millisecond,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    concurrency,
		},
	)

	ctx, cancel := context.WithTimeout(
		context.Background(), waitFor,
	)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	// The unlimited endpoint should finish quickly.
	waitForCondition(
		t,
		"unlimited endpoint delivered all events",
		3*time.Second,
		func() bool {
			return unlimitedCount.Load() == unlimitedTotal
		},
	)

	// The limited endpoint should still be working, not finished.
	if limitedCount.Load() >= limitedTotal {
		t.Errorf(
			"limited endpoint received %d requests within 3s at %d rps; "+
				"limit was not enforced",
			limitedCount.Load(),
			rateLimitRPS,
		)
	}

	// Wait for the worker to drain and shut down.
	cancel()
	<-done
}
