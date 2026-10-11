package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func serveEventListIntegrationRequest(
	t *testing.T,
	handler *EventsListHandler,
	path string,
	apiKey string,
) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder
}

func insertEventListFixture(
	t *testing.T,
	collection *mongo.Collection,
	id bson.ObjectID,
	status string,
	receivedAt time.Time,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := collection.InsertOne(ctx, bson.M{
		"_id":               id,
		"source":            "event-list-test",
		"event_type":        "test.created",
		"data":              bson.M{"test": true},
		"idempotency_key":   "list-" + id.Hex(),
		"received_at":       receivedAt,
		"status":            "accepted",
		"delivery_status":   status,
		"delivery_attempts": 1,
	})
	if err != nil {
		t.Fatalf("insert event-list fixture: %v", err)
	}
}

func TestEventListIntegrationFiltersByDeliveryStatus(t *testing.T) {
	db, _ := newMongoInspectionIntegration(t)
	collection := db.Collection("events")
	handler := NewEventsListHandler(collection, testAPIKey)
	now := time.Now().UTC().Truncate(time.Millisecond)

	insertEventListFixture(t, collection, bson.NewObjectID(), "failed", now)
	insertEventListFixture(t, collection, bson.NewObjectID(), "delivered", now.Add(-time.Second))
	insertEventListFixture(t, collection, bson.NewObjectID(), "failed", now.Add(-2*time.Second))

	recorder := serveEventListIntegrationRequest(
		t, handler, "/v1/events?delivery_status=failed&limit=20", testAPIKey,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response eventListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode event-list response: %v", err)
	}
	if len(response.Events) != 2 {
		t.Fatalf("expected 2 failed events, got %d", len(response.Events))
	}
	for _, event := range response.Events {
		if event.DeliveryStatus != "failed" {
			t.Errorf("expected only failed events, got status %q", event.DeliveryStatus)
		}
	}
}

func TestEventListIntegrationCursorPagination(t *testing.T) {
	db, _ := newMongoInspectionIntegration(t)
	collection := db.Collection("events")
	handler := NewEventsListHandler(collection, testAPIKey)
	base := time.Now().UTC().Truncate(time.Millisecond)

	for i := 0; i < 5; i++ {
		insertEventListFixture(
			t,
			collection,
			bson.NewObjectID(),
			"delivered",
			base.Add(time.Duration(i)*time.Second),
		)
	}

	first := serveEventListIntegrationRequest(
		t, handler, "/v1/events?limit=2", testAPIKey,
	)
	if first.Code != http.StatusOK {
		t.Fatalf("first page: expected 200, got %d: %s", first.Code, first.Body.String())
	}

	var page1 eventListResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(page1.Events) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", page1)
	}

	secondPath := fmt.Sprintf(
		"/v1/events?limit=2&cursor=%s",
		page1.NextCursor,
	)
	second := serveEventListIntegrationRequest(t, handler, secondPath, testAPIKey)
	if second.Code != http.StatusOK {
		t.Fatalf("second page: expected 200, got %d: %s", second.Code, second.Body.String())
	}

	var page2 eventListResponse
	if err := json.Unmarshal(second.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(page2.Events) != 2 || !page2.HasMore || page2.NextCursor == "" {
		t.Fatalf("unexpected second page: %+v", page2)
	}

	seen := make(map[string]bool)
	for _, event := range page1.Events {
		seen[event.ID] = true
	}
	for _, event := range page2.Events {
		if seen[event.ID] {
			t.Fatalf("duplicate event across pages: %s", event.ID)
		}
		seen[event.ID] = true
	}

	thirdPath := fmt.Sprintf(
		"/v1/events?limit=2&cursor=%s",
		page2.NextCursor,
	)
	third := serveEventListIntegrationRequest(t, handler, thirdPath, testAPIKey)
	if third.Code != http.StatusOK {
		t.Fatalf("third page: expected 200, got %d: %s", third.Code, third.Body.String())
	}

	var page3 eventListResponse
	if err := json.Unmarshal(third.Body.Bytes(), &page3); err != nil {
		t.Fatalf("decode third page: %v", err)
	}
	if len(page3.Events) != 1 || page3.HasMore {
		t.Fatalf("unexpected final page: %+v", page3)
	}
	for _, event := range page3.Events {
		if seen[event.ID] {
			t.Fatalf("duplicate event across pages: %s", event.ID)
		}
		seen[event.ID] = true
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 unique events across pages, got %d", len(seen))
	}
}
