package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/lifecycle"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// runWorkerOnce starts the worker in a goroutine and stops it after
// giving it enough time to complete one delivery cycle. Since the test
// is in package handlers, it cannot call the unexported processOne
// method; Run is the only legal entry point from here.
func runWorkerOnce(t *testing.T, worker *delivery.Worker, ctx context.Context) {
	t.Helper()

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(workerCtx)
	}()

	// Poll interval is 50ms in the test config; wait up to 3 seconds
	// for a delivery to be attempted and completed.
	time.Sleep(3 * time.Second)

	workerCancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop within 2 seconds of cancel")
	}
}

// TestLifecycleInvariantEndToEnd runs the full pipeline against real
// MongoDB and a real worker:
//
//	ingest -> fan-out -> deliver -> verify parent/child consistency
//	-> simulate delivery failure -> replay -> deliver -> verify
//
// At every state transition, the invariant asserted is contract §1:
// the aggregator is the sole writer of parent delivery_status, and the
// parent state must agree with the child deliveries.
func TestLifecycleInvariantEndToEnd(t *testing.T) {
	_, db := connectTestDB(t, "relay_lifecycle_invariant_e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events := db.Collection("events")
	endpointColl := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")

	// Receiver that fails the first call, then succeeds on subsequent
	// calls. This triggers replay.
	callsToFail := 1
	successCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callsToFail > 0 {
			callsToFail--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		successCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	now := time.Now().UTC()
	endpointID := bson.NewObjectID()
	if _, err := endpointColl.InsertOne(ctx, bson.M{
		"_id":          endpointID,
		"name":         "e2e-" + endpointID.Hex(),
		"url":          server.URL,
		"enabled":      true,
		"max_attempts": 1,
		"created_at":   now,
		"updated_at":   now,
	}); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	aggregator := lifecycle.NewAggregator(events, deliveries)

	eventID := bson.NewObjectID()
	if _, err := events.InsertOne(ctx, bson.M{
		"_id":             eventID,
		"source":          "e2e",
		"event_type":      "e2e.test",
		"data":            bson.M{"k": "v"},
		"idempotency_key": "e2e-" + eventID.Hex(),
		"received_at":     now,
		"status":          "accepted",
		"delivery_status": lifecycle.StatusManaged,
	}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}

	fanout := delivery.NewFanout(events, endpointColl, deliveries, aggregator, nil)
	if err := fanout.CreateDeliveries(ctx, eventID, "e2e", "e2e.test"); err != nil {
		t.Fatalf("fan-out: %v", err)
	}

	var parent bson.M
	if err := events.FindOne(ctx, bson.M{"_id": eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent after fan-out: %v", err)
	}
	statusAfterFanout, _ := parent["delivery_status"].(string)
	if statusAfterFanout != lifecycle.StatusProcessing && statusAfterFanout != lifecycle.StatusPending {
		t.Fatalf("parent after fan-out = %q; want processing or pending", statusAfterFanout)
	}

	worker := delivery.NewWorker(
		events, endpointColl, deliveries, attempts, aggregator,
		delivery.Config{
			PollInterval:   50 * time.Millisecond,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    1,
		},
	)

	// First delivery: fails (max_attempts=1, so terminal after one failure).
	runWorkerOnce(t, worker, ctx)

	var child bson.M
	if err := deliveries.FindOne(ctx, bson.M{"event_id": eventID}).Decode(&child); err != nil {
		t.Fatalf("read child: %v", err)
	}
	if child["status"] != "failed" {
		t.Fatalf("child status = %v; want failed", child["status"])
	}

	if err := events.FindOne(ctx, bson.M{"_id": eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent after failure: %v", err)
	}
	if parent["delivery_status"] != lifecycle.StatusFailed {
		t.Fatalf("parent after delivery failure = %v; want failed", parent["delivery_status"])
	}

	// Replay: requeue the failed child.
	eventsHandler := NewEventsHandler(events, deliveries, testAPIKey, nil, aggregator)
	mux := http.NewServeMux()
	eventsHandler.Register(mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/events/"+eventID.Hex()+"/replay",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("replay: expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	if err := deliveries.FindOne(ctx, bson.M{"event_id": eventID}).Decode(&child); err != nil {
		t.Fatalf("read child after replay: %v", err)
	}
	if child["status"] != "pending" {
		t.Errorf("child after replay = %v; want pending", child["status"])
	}
	if got, _ := child["replay_cycle"].(int32); got != 1 {
		t.Errorf("child replay_cycle = %v; want 1", got)
	}

	// Second delivery: succeeds.
	runWorkerOnce(t, worker, ctx)

	if successCount < 1 {
		t.Errorf("receiver success count = %d; want >= 1", successCount)
	}

	if err := deliveries.FindOne(ctx, bson.M{"event_id": eventID}).Decode(&child); err != nil {
		t.Fatalf("read final child: %v", err)
	}
	if child["status"] != "delivered" {
		t.Errorf("final child status = %v; want delivered", child["status"])
	}

	if err := events.FindOne(ctx, bson.M{"_id": eventID}).Decode(&parent); err != nil {
		t.Fatalf("read final parent: %v", err)
	}
	if parent["delivery_status"] != lifecycle.StatusDelivered {
		t.Errorf("final parent status = %v; want delivered", parent["delivery_status"])
	}

	attemptCount, err := attempts.CountDocuments(ctx, bson.M{"event_id": eventID})
	if err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attemptCount != 2 {
		t.Errorf("attempt count = %d; want 2 (one failed, one replayed)", attemptCount)
	}

	cursor, err := attempts.Find(ctx, bson.M{"event_id": eventID})
	if err != nil {
		t.Fatalf("find attempts: %v", err)
	}
	defer cursor.Close(ctx)

	cycles := make(map[int]bool)
	for cursor.Next(ctx) {
		var a delivery.Attempt
		if err := cursor.Decode(&a); err != nil {
			t.Fatalf("decode attempt: %v", err)
		}
		cycles[a.ReplayCycle] = true
	}
	if len(cycles) != 2 {
		t.Errorf("distinct replay_cycles = %d; want 2 (original + replayed)", len(cycles))
	}
}
