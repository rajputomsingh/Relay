package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/lifecycle"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func getTestMongoURI() string {
	return os.Getenv("MONGODB_TEST_URI")
}

func connectTestDB(t *testing.T, dbName string) (*mongo.Client, *mongo.Database) {
	t.Helper()

	uri := getTestMongoURI()
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		t.Fatalf("ping mongo: %v", err)
	}

	db := client.Database(dbName)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = db.Drop(cleanupCtx)
		_ = client.Disconnect(cleanupCtx)
	})

	return client, db
}

type replayFixture struct {
	db         *mongo.Database
	events     *mongo.Collection
	endpoints  *mongo.Collection
	deliveries *mongo.Collection
	attempts   *mongo.Collection
	handler    *EventsHandler
	worker     *delivery.Worker
	eventID    bson.ObjectID
}

func newReplayFixture(
	t *testing.T,
	dbName string,
	deliveredCount, failedCount int,
) *replayFixture {
	t.Helper()

	_, db := connectTestDB(t, dbName)

	events := db.Collection("events")
	endpointColl := db.Collection("endpoints")
	deliveries := db.Collection("deliveries")
	attempts := db.Collection("delivery_attempts")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UTC()

	endpointID := bson.NewObjectID()
	if _, err := endpointColl.InsertOne(ctx, bson.M{
		"_id":          endpointID,
		"name":         "replay-" + endpointID.Hex(),
		"url":          srv.URL,
		"enabled":      true,
		"max_attempts": 3,
		"created_at":   now,
		"updated_at":   now,
	}); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	eventID := bson.NewObjectID()
	if _, err := events.InsertOne(ctx, bson.M{
		"_id":                   eventID,
		"source":                "replay-test",
		"event_type":            "test.replay",
		"data":                  bson.M{"test": true},
		"idempotency_key":       "replay-" + eventID.Hex(),
		"received_at":           now,
		"status":                "accepted",
		"delivery_status":       lifecycle.StatusFailed,
		"delivery_attempts":     deliveredCount + failedCount,
		"fanout_status":         "completed",
		"fanout_expected_count": deliveredCount + failedCount,
		"fanout_completed_at":   now,
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	for i := 0; i < deliveredCount; i++ {
		epID := bson.NewObjectID()
		if _, err := endpointColl.InsertOne(ctx, bson.M{
			"_id":          epID,
			"name":         "delivered-" + epID.Hex(),
			"url":          srv.URL,
			"enabled":      true,
			"max_attempts": 3,
			"created_at":   now,
			"updated_at":   now,
		}); err != nil {
			t.Fatalf("seed delivered endpoint: %v", err)
		}
		if _, err := deliveries.InsertOne(ctx, bson.M{
			"_id":          bson.NewObjectID(),
			"event_id":     eventID,
			"endpoint_id":  epID,
			"status":       "delivered",
			"attempts":     1,
			"max_attempts": 3,
			"delivered_at": now,
			"created_at":   now,
			"updated_at":   now,
		}); err != nil {
			t.Fatalf("seed delivered child: %v", err)
		}
	}

	for i := 0; i < failedCount; i++ {
		if _, err := deliveries.InsertOne(ctx, bson.M{
			"_id":          bson.NewObjectID(),
			"event_id":     eventID,
			"endpoint_id":  endpointID,
			"status":       "failed",
			"attempts":     3,
			"max_attempts": 3,
			"failed_at":    now,
			"last_error":   "prior failure",
			"created_at":   now,
			"updated_at":   now,
		}); err != nil {
			t.Fatalf("seed failed child: %v", err)
		}
	}

	aggregator := lifecycle.NewAggregator(events, deliveries)
	worker := delivery.NewWorker(
		events, endpointColl, deliveries, attempts, aggregator,
		delivery.Config{
			PollInterval:   50 * time.Millisecond,
			RequestTimeout: 3 * time.Second,
			LeaseDuration:  30 * time.Second,
			Concurrency:    1,
		},
	)

	handler := NewEventsHandler(events, deliveries, testAPIKey, nil, aggregator)

	return &replayFixture{
		db:         db,
		events:     events,
		endpoints:  endpointColl,
		deliveries: deliveries,
		attempts:   attempts,
		handler:    handler,
		worker:     worker,
		eventID:    eventID,
	}
}

func (f *replayFixture) serveReplay(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	f.handler.Register(mux)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/events/"+f.eventID.Hex()+"/replay",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestReplayRequeuesFailedChildrenOnly(t *testing.T) {
	f := newReplayFixture(t, "relay_replay_requeue_only", 2, 1)
	ctx := context.Background()

	rec := f.serveReplay(t)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if n, _ := resp["deliveries_requeued"].(float64); int(n) != 1 {
		t.Errorf("deliveries_requeued = %v, want 1", resp["deliveries_requeued"])
	}

	deliveredCount, _ := f.deliveries.CountDocuments(ctx, bson.M{
		"event_id": f.eventID, "status": "delivered",
	})
	if deliveredCount != 2 {
		t.Errorf("delivered children = %d, want 2 (untouched)", deliveredCount)
	}

	pendingCount, _ := f.deliveries.CountDocuments(ctx, bson.M{
		"event_id": f.eventID, "status": "pending",
	})
	if pendingCount != 1 {
		t.Errorf("pending children = %d, want 1", pendingCount)
	}

	var child bson.M
	if err := f.deliveries.FindOne(ctx, bson.M{
		"event_id": f.eventID, "status": "pending",
	}).Decode(&child); err != nil {
		t.Fatalf("read pending child: %v", err)
	}
	if got, _ := child["replay_cycle"].(int32); got != 1 {
		t.Errorf("child replay_cycle = %v, want 1", got)
	}
	if got, _ := child["attempts"].(int32); got != 0 {
		t.Errorf("child attempts = %v, want 0 (reset)", got)
	}

	var parent bson.M
	if err := f.events.FindOne(ctx, bson.M{"_id": f.eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	status, _ := parent["delivery_status"].(string)
	if status != lifecycle.StatusProcessing && status != lifecycle.StatusPending {
		t.Errorf("parent status = %q, want processing or pending", status)
	}
	if got, _ := parent["replay_count"].(int32); got != 1 {
		t.Errorf("replay_count = %v, want 1", got)
	}
}

func TestReplayEndToEndDeliversRequeuedChildren(t *testing.T) {
	f := newReplayFixture(t, "relay_replay_end_to_end", 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rec := f.serveReplay(t)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go f.worker.Run(workerCtx)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		delivered, _ := f.deliveries.CountDocuments(ctx, bson.M{
			"event_id": f.eventID, "status": "delivered",
		})
		if delivered == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	workerCancel()
	time.Sleep(100 * time.Millisecond)

	delivered, _ := f.deliveries.CountDocuments(ctx, bson.M{
		"event_id": f.eventID, "status": "delivered",
	})
	if delivered != 2 {
		t.Fatalf("delivered = %d, want 2 (one pre-existing + one replayed)", delivered)
	}

	var parent bson.M
	if err := f.events.FindOne(ctx, bson.M{"_id": f.eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if got := parent["delivery_status"]; got != lifecycle.StatusDelivered {
		t.Errorf("parent status = %v, want delivered", got)
	}
}

func TestReplayRejectsIneligibleStates(t *testing.T) {
	f := newReplayFixture(t, "relay_replay_ineligible", 1, 0)
	ctx := context.Background()

	for _, status := range []string{
		lifecycle.StatusPending,
		lifecycle.StatusProcessing,
		lifecycle.StatusDelivered,
		lifecycle.StatusDisabled,
		lifecycle.StatusNoDestination,
	} {
		t.Run(status, func(t *testing.T) {
			if _, err := f.events.UpdateOne(
				ctx,
				bson.M{"_id": f.eventID},
				bson.M{"$set": bson.M{"delivery_status": status}},
			); err != nil {
				t.Fatalf("set status: %v", err)
			}
			rec := f.serveReplay(t)
			if rec.Code != http.StatusConflict {
				t.Errorf("expected 409 for %s, got %d", status, rec.Code)
			}
		})
	}
}

func TestReplayConcurrentRequestsHaveOneWinner(t *testing.T) {
	f := newReplayFixture(t, "relay_replay_concurrent", 1, 1)
	ctx := context.Background()

	const requestCount = 2
	statuses := make(chan int, requestCount)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := f.serveReplay(t)
			statuses <- rec.Code
		}()
	}

	close(start)
	wg.Wait()
	close(statuses)

	accepted := 0
	conflicts := 0
	for s := range statuses {
		switch s {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status: %d", s)
		}
	}

	if accepted != 1 || conflicts != 1 {
		t.Fatalf("expected one 202 + one 409, got %d/%d", accepted, conflicts)
	}

	var parent bson.M
	if err := f.events.FindOne(ctx, bson.M{"_id": f.eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if got, _ := parent["replay_count"].(int32); got != 1 {
		t.Errorf("replay_count = %v, want exactly 1 (one winner)", got)
	}
}
