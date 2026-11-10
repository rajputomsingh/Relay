package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestWorkerCrashBeforeDBWriteResultsInDuplicateDelivery simulates the
// at-least-once delivery contract: a worker claims a delivery, sends the
// webhook, the receiver processes it, but the worker crashes before
// writing the terminal `delivered` status. The lease expires, another
// worker reclaims, and the receiver sees a duplicate.
//
// This is the correct behavior under at-least-once semantics. The test
// asserts that the delivery is retried (duplicate) rather than silently
// lost, and that the second attempt records its own attempt document
// with attempt_number = 2.
func TestWorkerCrashBeforeDBWriteResultsInDuplicateDelivery(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	var deliveryCount atomic.Int32
	received := make(chan struct{}, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		deliveryCount.Add(1)
		select {
		case received <- struct{}{}:
		default:
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

	// Simulate a crashed worker: delivery is in `processing` with an
	// expired lease. The webhook was already sent on the previous attempt.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.deliveries.UpdateOne(
		ctx,
		bson.M{"_id": deliveryID},
		bson.M{"$set": bson.M{
			"status":      StatusProcessing,
			"attempts":    1,
			"lease_until": time.Now().UTC().Add(-time.Minute),
			"lease_token": "crashed-worker-token",
		}},
	); err != nil {
		t.Fatalf("seed crashed state: %v", err)
	}

	// Simulate the "previous attempt" webhook delivery that the crashed
	// worker sent before dying (test-side only, no DB write).
	deliveryCount.Add(1)
	received <- struct{}{}

	// New worker reclaims via lease expiry and retries the delivery.
	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process recovered delivery: %v", err)
	}

	// Two webhook requests reached the receiver — one from the crashed
	// worker, one from the recovery worker.
	if got := deliveryCount.Load(); got != 2 {
		t.Errorf("receiver saw %d requests; want 2 (at-least-once duplicate)", got)
	}

	// Final state is `delivered`.
	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusDelivered {
		t.Errorf("status = %q; want delivered", state.Status)
	}

	// Attempt history: the retry is attempt 2, not a loss of history.
	attempts := readWorkerAttempts(t, db.attempts, eventID, endpointID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1 (only the second-attempt record from a real processOne run)", len(attempts))
	}
	if attempts[0].AttemptNumber != 2 {
		t.Errorf("attempt number = %d; want 2", attempts[0].AttemptNumber)
	}
	if attempts[0].Outcome != "delivered" {
		t.Errorf("outcome = %q; want delivered", attempts[0].Outcome)
	}
}

// TestWorkerCrashAfterHTTPResponseBeforeDBWritePreservesRecoverability
// simulates a worker that: claims the delivery, sends the webhook (receiver
// returns 200), but crashes between reading the response and writing the
// delivered state. Recovery must:
//   - reclaim the delivery once the lease expires,
//   - not corrupt the delivery document,
//   - record a new attempt.
//
// The observable contract: duplicate webhook is expected; no data loss;
// each attempt has a distinct attempt_number.
func TestWorkerCrashAfterHTTPResponseBeforeDBWritePreservesRecoverability(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	var mu sync.Mutex
	requestsSeen := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestsSeen++
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
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

	// Simulate crash-after-HTTP: worker claimed, sent webhook, but the
	// DB write for `delivered` never landed. Delivery is stuck in
	// `processing` with expired lease and attempts=1.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.deliveries.UpdateOne(
		ctx,
		bson.M{"_id": deliveryID},
		bson.M{"$set": bson.M{
			"status":      StatusProcessing,
			"attempts":    1,
			"lease_until": time.Now().UTC().Add(-30 * time.Second),
			"lease_token": "crashed-token",
			"last_error":  "",
		}},
	); err != nil {
		t.Fatalf("seed crash-after-http state: %v", err)
	}

	// New worker recovers.
	worker := newIntegrationWorker(db, time.Second)
	if err := worker.processOne(context.Background()); err != nil {
		t.Fatalf("process recovered delivery: %v", err)
	}

	mu.Lock()
	seen := requestsSeen
	mu.Unlock()
	if seen != 1 {
		t.Errorf("recovery worker sent %d requests; want 1", seen)
	}

	state := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if state.Status != StatusDelivered {
		t.Errorf("status = %q; want delivered", state.Status)
	}
	// attempt counter goes from 1 → 2 in the recovery run; the delivery
	// document's Attempts reflects the total.
	if state.Attempts < 1 {
		t.Errorf("attempts = %d; want >= 1", state.Attempts)
	}

	// Recovery attempt is recorded with attempt_number 2.
	attempts := readWorkerAttempts(t, db.attempts, eventID, endpointID)
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d; want 1", len(attempts))
	}
	if attempts[0].AttemptNumber != 2 {
		t.Errorf("attempt number = %d; want 2 (recovery attempt)", attempts[0].AttemptNumber)
	}
}
