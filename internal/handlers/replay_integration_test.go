package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func insertReplayIntegrationEvent(
	t *testing.T,
	collection *mongo.Collection,
	id bson.ObjectID,
	status string,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	event := bson.M{
		"_id":                 id,
		"source":              "replay-integration-test",
		"event_type":          "test.replay",
		"data":                bson.M{"test": true},
		"idempotency_key":     "replay-" + id.Hex(),
		"received_at":         now,
		"status":              "accepted",
		"delivery_status":     status,
		"delivery_attempts":   3,
		"failed_at":           now,
		"last_delivery_error": "integration test failure",
		"lease_until":         now.Add(time.Minute),
	}

	if status == "delivered" {
		event["delivered_at"] = now
	}

	if _, err := collection.InsertOne(ctx, event); err != nil {
		t.Fatalf("insert replay fixture: %v", err)
	}
}

func serveReplayIntegrationRequest(
	mux http.Handler,
	id bson.ObjectID,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/events/"+id.Hex()+"/replay",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder
}

func TestReplayIntegrationSuccessPreservesEventIdentity(t *testing.T) {
	db, _ := newMongoInspectionIntegration(t)
	collection := db.Collection("events")
	id := bson.NewObjectID()

	insertReplayIntegrationEvent(t, collection, id, "failed")

	mux := http.NewServeMux()
	NewEventsHandler(collection, testAPIKey, true).Register(mux)

	recorder := serveReplayIntegrationRequest(mux, id)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"replay_queued"`) {
		t.Fatalf("expected replay_queued response, got %s", recorder.Body.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var event bson.M
	if err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&event); err != nil {
		t.Fatalf("read replayed event: %v", err)
	}

	if event["delivery_status"] != "pending" {
		t.Errorf("delivery_status = %v, want pending", event["delivery_status"])
	}
	if got := fmt.Sprint(event["delivery_attempts"]); got != "0" {
		t.Errorf("delivery_attempts = %s, want 0", got)
	}
	if event["idempotency_key"] != "replay-"+id.Hex() {
		t.Errorf("idempotency_key changed: %v", event["idempotency_key"])
	}
	if got := fmt.Sprint(event["replay_count"]); got != "1" {
		t.Errorf("replay_count = %s, want 1", got)
	}

	// The MongoDB Go driver decodes BSON dates as bson.DateTime.
	replayedAt, ok := event["last_replayed_at"].(bson.DateTime)
	if !ok {
		t.Fatalf(
			"last_replayed_at is missing or has the wrong type: %T",
			event["last_replayed_at"],
		)
	}
	if replayedAt <= 0 {
		t.Errorf("last_replayed_at should be a valid timestamp, got %v", replayedAt)
	}

	for _, field := range []string{
		"failed_at",
		"delivered_at",
		"last_delivery_error",
		"lease_until",
	} {
		if _, exists := event[field]; exists {
			t.Errorf("%s should be unset after replay, got %v", field, event[field])
		}
	}
}

func TestReplayIntegrationRejectsIneligibleStates(t *testing.T) {
	db, _ := newMongoInspectionIntegration(t)
	collection := db.Collection("events")

	mux := http.NewServeMux()
	NewEventsHandler(collection, testAPIKey, true).Register(mux)

	for _, status := range []string{"pending", "processing", "delivered"} {
		t.Run(status, func(t *testing.T) {
			id := bson.NewObjectID()
			insertReplayIntegrationEvent(t, collection, id, status)

			recorder := serveReplayIntegrationRequest(mux, id)
			if recorder.Code != http.StatusConflict {
				t.Fatalf(
					"expected 409 for %s event, got %d: %s",
					status,
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestReplayIntegrationConcurrentRequestsHaveOneWinner(t *testing.T) {
	db, _ := newMongoInspectionIntegration(t)
	collection := db.Collection("events")
	id := bson.NewObjectID()

	insertReplayIntegrationEvent(t, collection, id, "failed")

	mux := http.NewServeMux()
	NewEventsHandler(collection, testAPIKey, true).Register(mux)

	const requestCount = 2
	statuses := make(chan int, requestCount)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := 0; i < requestCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			recorder := serveReplayIntegrationRequest(mux, id)
			statuses <- recorder.Code
		}()
	}

	close(start)
	wg.Wait()
	close(statuses)

	accepted := 0
	conflicts := 0

	for status := range statuses {
		switch status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected concurrent replay status: %d", status)
		}
	}

	if accepted != 1 || conflicts != 1 {
		t.Fatalf(
			"expected one 202 and one 409; got accepted=%d conflicts=%d",
			accepted,
			conflicts,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var event bson.M
	if err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&event); err != nil {
		t.Fatalf("read event after concurrent replay: %v", err)
	}

	if got := fmt.Sprint(event["replay_count"]); got != "1" {
		t.Errorf("replay_count = %s, want 1", got)
	}
	if event["delivery_status"] != "pending" {
		t.Errorf("delivery_status = %v, want pending", event["delivery_status"])
	}
}
