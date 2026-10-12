package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type workerIntegrationDB struct {
	events   *mongo.Collection
	attempts *mongo.Collection
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
		events:   db.Collection("events"),
		attempts: db.Collection("delivery_attempts"),
	}
}

func insertWorkerIntegrationEvent(
	t *testing.T,
	collection *mongo.Collection,
) bson.ObjectID {
	t.Helper()

	id := bson.NewObjectID()
	now := time.Now().UTC()

	event := bson.M{
		"_id":               id,
		"source":            "integration-test",
		"event_type":        "invoice.paid",
		"data":              bson.M{"invoice_id": "inv-worker-test"},
		"idempotency_key":   "worker-test-" + id.Hex(),
		"received_at":       now,
		"status":            "accepted",
		"delivery_status":   "pending",
		"delivery_attempts": 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := collection.InsertOne(ctx, event); err != nil {
		t.Fatalf("insert integration event: %v", err)
	}

	return id
}

type workerEventState struct {
	DeliveryStatus    string     `bson:"delivery_status"`
	DeliveryAttempts  int        `bson:"delivery_attempts"`
	DeliveredAt       *time.Time `bson:"delivered_at"`
	FailedAt          *time.Time `bson:"failed_at"`
	NextAttemptAt     *time.Time `bson:"next_attempt_at"`
	LeaseUntil        *time.Time `bson:"lease_until"`
	LastDeliveryError string     `bson:"last_delivery_error"`
}

func readWorkerEventState(
	t *testing.T,
	collection *mongo.Collection,
	id bson.ObjectID,
) workerEventState {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var state workerEventState
	if err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&state); err != nil {
		t.Fatalf("read event state: %v", err)
	}

	return state
}

func readWorkerAttempts(
	t *testing.T,
	collection *mongo.Collection,
	eventID bson.ObjectID,
) []Attempt {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := collection.Find(ctx, bson.M{"event_id": eventID})
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

func newIntegrationWorker(
	serverURL string,
	maxAttempts int,
	db workerIntegrationDB,
) *Worker {
	return NewWorker(db.events, db.attempts, Config{
		WebhookURL:     serverURL,
		MaxAttempts:    maxAttempts,
		RequestTimeout: 3 * time.Second,
		LeaseDuration:  30 * time.Second,
	})
}

func TestWorkerIntegrationSuccessfulDeliveryAndAttemptHistory(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		if r.Method != http.MethodPost {
			t.Errorf("request method = %q; want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q; want application/json", got)
		}
		if got := r.Header.Get("Idempotency-Key"); !strings.HasPrefix(got, "worker-test-") {
			t.Errorf("unexpected Idempotency-Key: %q", got)
		}
		if r.Header.Get("X-Relay-Event-ID") == "" {
			t.Error("missing X-Relay-Event-ID header")
		}

		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		}
		if payload.EventType != "invoice.paid" {
			t.Errorf("payload event type = %q; want invoice.paid", payload.EventType)
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	worker := newIntegrationWorker(server.URL, 3, db)

	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process event: %v", err)
	}

	if requestCount != 1 {
		t.Fatalf("webhook request count = %d; want 1", requestCount)
	}

	state := readWorkerEventState(t, db.events, eventID)
	if state.DeliveryStatus != "delivered" {
		t.Errorf("delivery status = %q; want delivered", state.DeliveryStatus)
	}
	if state.DeliveryAttempts != 1 {
		t.Errorf("delivery attempts = %d; want 1", state.DeliveryAttempts)
	}
	if state.DeliveredAt == nil || state.DeliveredAt.IsZero() {
		t.Error("expected delivered_at to be set")
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1", len(attempts))
	}
	if attempts[0].AttemptNumber != 1 {
		t.Errorf("attempt number = %d; want 1", attempts[0].AttemptNumber)
	}
	if attempts[0].HTTPStatus != http.StatusNoContent {
		t.Errorf("HTTP status = %d; want %d", attempts[0].HTTPStatus, http.StatusNoContent)
	}
	if attempts[0].Outcome != "delivered" {
		t.Errorf("attempt outcome = %q; want delivered", attempts[0].Outcome)
	}
	if attempts[0].FinishedAt.IsZero() {
		t.Error("expected attempt finished_at to be set")
	}
}

func TestWorkerIntegrationTemporaryFailureSchedulesRetry(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	worker := newIntegrationWorker(server.URL, 3, db)

	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process event: %v", err)
	}

	state := readWorkerEventState(t, db.events, eventID)
	if state.DeliveryStatus != "pending" {
		t.Errorf("delivery status = %q; want pending", state.DeliveryStatus)
	}
	if state.DeliveryAttempts != 1 {
		t.Errorf("delivery attempts = %d; want 1", state.DeliveryAttempts)
	}
	if state.NextAttemptAt == nil {
		t.Fatal("expected next_attempt_at to be set")
	}
	if !state.NextAttemptAt.After(time.Now().UTC()) {
		t.Errorf("next_attempt_at = %v; want a future time", *state.NextAttemptAt)
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if !strings.Contains(state.LastDeliveryError, "503") {
		t.Errorf("last delivery error = %q; want HTTP 503", state.LastDeliveryError)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1", len(attempts))
	}
	if attempts[0].HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTP status = %d; want %d", attempts[0].HTTPStatus, http.StatusServiceUnavailable)
	}
	if attempts[0].Outcome != "failed" {
		t.Errorf("attempt outcome = %q; want failed", attempts[0].Outcome)
	}
	if attempts[0].Error == "" {
		t.Error("expected attempt error to be recorded")
	}
}

func TestWorkerIntegrationFailsAfterMaximumAttempts(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	worker := newIntegrationWorker(server.URL, 1, db)

	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process event: %v", err)
	}

	state := readWorkerEventState(t, db.events, eventID)
	if state.DeliveryStatus != "failed" {
		t.Errorf("delivery status = %q; want failed", state.DeliveryStatus)
	}
	if state.DeliveryAttempts != 1 {
		t.Errorf("delivery attempts = %d; want 1", state.DeliveryAttempts)
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
	if !strings.Contains(state.LastDeliveryError, "500") {
		t.Errorf("last delivery error = %q; want HTTP 500", state.LastDeliveryError)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1", len(attempts))
	}
	if attempts[0].Outcome != "failed" {
		t.Errorf("attempt outcome = %q; want failed", attempts[0].Outcome)
	}
	if attempts[0].HTTPStatus != http.StatusInternalServerError {
		t.Errorf("HTTP status = %d; want %d", attempts[0].HTTPStatus, http.StatusInternalServerError)
	}
}

func TestWorkerIntegrationReclaimsExpiredLease(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	now := time.Now().UTC()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.events.UpdateOne(ctx, bson.M{"_id": eventID}, bson.M{
		"$set": bson.M{
			"delivery_status":   "processing",
			"delivery_attempts": 1,
			"lease_until":       now.Add(-time.Minute),
			"last_attempt_at":   now.Add(-2 * time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("prepare expired lease: %v", err)
	}

	worker := newIntegrationWorker(server.URL, 3, db)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process expired lease: %v", err)
	}

	state := readWorkerEventState(t, db.events, eventID)
	if state.DeliveryStatus != "delivered" {
		t.Errorf("delivery status = %q; want delivered", state.DeliveryStatus)
	}
	if state.DeliveryAttempts != 2 {
		t.Errorf("delivery attempts = %d; want 2", state.DeliveryAttempts)
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if requestCount != 1 {
		t.Errorf("webhook requests = %d; want 1", requestCount)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1 new attempt", len(attempts))
	}
	if attempts[0].AttemptNumber != 2 {
		t.Errorf("attempt number = %d; want 2", attempts[0].AttemptNumber)
	}
	if attempts[0].Outcome != "delivered" {
		t.Errorf("attempt outcome = %q; want delivered", attempts[0].Outcome)
	}
}

func TestWorkerIntegrationDoesNotReclaimActiveLease(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	now := time.Now().UTC()
	leaseUntil := now.Add(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.events.UpdateOne(ctx, bson.M{"_id": eventID}, bson.M{
		"$set": bson.M{
			"delivery_status":   "processing",
			"delivery_attempts": 1,
			"lease_until":       leaseUntil,
		},
	})
	if err != nil {
		t.Fatalf("prepare active lease: %v", err)
	}

	worker := newIntegrationWorker(server.URL, 3, db)
	err = worker.processOne(context.Background())
	if err != mongo.ErrNoDocuments {
		t.Fatalf("process active lease error = %v; want mongo.ErrNoDocuments", err)
	}

	state := readWorkerEventState(t, db.events, eventID)
	if state.DeliveryStatus != "processing" {
		t.Errorf("delivery status = %q; want processing", state.DeliveryStatus)
	}
	if state.DeliveryAttempts != 1 {
		t.Errorf("delivery attempts = %d; want 1", state.DeliveryAttempts)
	}

	if state.LeaseUntil == nil ||
		state.LeaseUntil.UnixMilli() != leaseUntil.UnixMilli() {
		t.Errorf("lease_until = %v; want %v", state.LeaseUntil, leaseUntil)
	}
	if requestCount != 0 {
		t.Errorf("webhook requests = %d; want 0", requestCount)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID)
	if len(attempts) != 0 {
		t.Errorf("persisted attempts = %d; want 0", len(attempts))
	}
}

func TestWorkerIntegrationFailsExpiredExhaustedLease(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	eventID := insertWorkerIntegrationEvent(t, db.events)
	now := time.Now().UTC()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.events.UpdateOne(ctx, bson.M{"_id": eventID}, bson.M{
		"$set": bson.M{
			"delivery_status":   "processing",
			"delivery_attempts": 3,
			"lease_until":       now.Add(-time.Minute),
			"next_attempt_at":   now.Add(-2 * time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("prepare exhausted lease: %v", err)
	}

	worker := newIntegrationWorker(server.URL, 3, db)
	err = worker.processOne(context.Background())
	if err != mongo.ErrNoDocuments {
		t.Fatalf("process exhausted lease error = %v; want mongo.ErrNoDocuments", err)
	}

	state := readWorkerEventState(t, db.events, eventID)
	if state.DeliveryStatus != "failed" {
		t.Errorf("delivery status = %q; want failed", state.DeliveryStatus)
	}
	if state.DeliveryAttempts != 3 {
		t.Errorf("delivery attempts = %d; want 3", state.DeliveryAttempts)
	}
	if state.FailedAt == nil || state.FailedAt.IsZero() {
		t.Error("expected failed_at to be set")
	}
	if state.LeaseUntil != nil {
		t.Errorf("lease_until = %v; want unset", state.LeaseUntil)
	}
	if state.NextAttemptAt != nil {
		t.Errorf("next_attempt_at = %v; want unset", state.NextAttemptAt)
	}
	if !strings.Contains(state.LastDeliveryError, "expired after maximum attempts") {
		t.Errorf("last delivery error = %q; want lease-expiration error", state.LastDeliveryError)
	}
	if requestCount != 0 {
		t.Errorf("webhook requests = %d; want 0", requestCount)
	}

	attempts := readWorkerAttempts(t, db.attempts, eventID)
	if len(attempts) != 0 {
		t.Errorf("persisted attempts = %d; want 0 additional attempts", len(attempts))
	}
}

func TestWorkerIntegrationPreventsDuplicateConcurrentClaims(t *testing.T) {
	db := newWorkerIntegrationDB(t)
	eventID := insertWorkerIntegrationEvent(t, db.events)

	const workerCount = 16

	start := make(chan struct{})
	results := make(chan error, workerCount)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			worker := newIntegrationWorker("http://127.0.0.1:1", 3, db)

			<-start

			ctx, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
			defer cancel()

			event, err := worker.claim(ctx)
			if err == nil {
				if event.ID != eventID {
					results <- fmt.Errorf(
						"claimed event ID = %s; want %s",
						event.ID.Hex(),
						eventID.Hex(),
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
			noDocuments,
			workerCount-1,
		)
	}

	state := readWorkerEventState(t, db.events, eventID)

	if state.DeliveryStatus != "processing" {
		t.Errorf(
			"delivery status = %q; want processing",
			state.DeliveryStatus,
		)
	}

	if state.DeliveryAttempts != 1 {
		t.Errorf(
			"delivery attempts = %d; want 1",
			state.DeliveryAttempts,
		)
	}

	if state.LeaseUntil == nil || !state.LeaseUntil.After(time.Now()) {
		t.Errorf("expected a future lease; got %v", state.LeaseUntil)
	}
}

func TestWorkerIntegrationCancellationPreservesRecoverableEvent(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		select {
		case <-requestCancelled:
		default:
			close(requestCancelled)
		}
	}))
	defer server.Close()

	id := insertWorkerIntegrationEvent(t, db.events)
	worker := newIntegrationWorker(server.URL, 3, db)

	ctx, cancel := context.WithCancel(context.Background())
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

	select {
	case <-requestCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook request context was not cancelled")
	}

	state := readWorkerEventState(t, db.events, id)

	if state.DeliveryStatus == "delivered" {
		t.Fatal("event marked delivered despite no webhook acknowledgement")
	}

	if state.DeliveryStatus != "processing" {
		t.Fatalf(
			"delivery status = %q; want processing until lease recovery",
			state.DeliveryStatus,
		)
	}

	if state.LeaseUntil == nil {
		t.Fatal("expected the interrupted event to retain its lease")
	}

	if state.DeliveryAttempts != 1 {
		t.Fatalf(
			"delivery attempts = %d; want 1",
			state.DeliveryAttempts,
		)
	}
}
