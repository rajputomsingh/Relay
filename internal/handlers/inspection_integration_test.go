package handlers

import (
	"context"

	"encoding/json"

	"fmt"

	"net/http"

	"net/http/httptest"

	"os"

	"strings"

	"testing"

	"time"

	"github.com/rajputomsingh/relay/internal/delivery"

	"go.mongodb.org/mongo-driver/v2/bson"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func newMongoInspectionIntegration(t *testing.T) (*mongo.Database, *InspectionHandler) {

	t.Helper()

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))

	if uri == "" {

		t.Skip("set MONGODB_TEST_URI to run MongoDB inspection integration tests")

	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	defer cancel()

	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-inspection-integration-tests").
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

	// Use a unique database for each test. Cleanup drops only this database.

	dbName := fmt.Sprintf("rit_%x", time.Now().UnixNano())

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

	handler := NewInspectionHandler(
		db.Collection("events"),
		db.Collection("delivery_attempts"),
		db.Collection("deliveries"),
		testAPIKey,
	)
	return db, handler

}

func serveInspectionIntegrationRequest(

	t *testing.T,

	handler *InspectionHandler,

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

func insertIntegrationEvent(

	t *testing.T,

	db *mongo.Database,

	id bson.ObjectID,

) {

	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	event := bson.M{

		"_id": id,

		"source": "integration-test",

		"event_type": "invoice.paid",

		"data": bson.M{"invoice_id": "inv-integration-001"},

		"idempotency_key": "integration-" + id.Hex(),

		"received_at": time.Now().UTC(),

		"status": "accepted",

		"delivery_status": "delivered",

		"delivery_attempts": 3,
	}

	if _, err := db.Collection("events").InsertOne(ctx, event); err != nil {

		t.Fatalf("insert integration event: %v", err)

	}

}

func insertIntegrationAttempts(

	t *testing.T,

	db *mongo.Database,

	eventID bson.ObjectID,

	count int,

) []delivery.Attempt {

	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	now := time.Now().UTC().Truncate(time.Millisecond)

	attempts := make([]delivery.Attempt, 0, count)

	documents := make([]any, 0, count)

	// Deliberately create distinct start times to make cursor ordering clear.

	for i := 0; i < count; i++ {

		attempt := delivery.Attempt{

			ID: bson.NewObjectID(),

			EventID: eventID,

			AttemptNumber: count - i,

			StartedAt: now.Add(-time.Duration(i) * time.Minute),

			FinishedAt: now.Add(-time.Duration(i)*time.Minute + time.Second),

			DurationMS: 100 + int64(i),

			HTTPStatus: 200,

			Outcome: "delivered",
		}

		attempts = append(attempts, attempt)

		documents = append(documents, attempt)

	}

	if len(documents) > 0 {

		if _, err := db.Collection("delivery_attempts").InsertMany(ctx, documents); err != nil {

			t.Fatalf("insert integration attempts: %v", err)

		}

	}

	return attempts

}

func TestInspectionIntegrationGetEvent(t *testing.T) {

	db, handler := newMongoInspectionIntegration(t)

	eventID := bson.NewObjectID()

	insertIntegrationEvent(t, db, eventID)

	recorder := serveInspectionIntegrationRequest(

		t, handler, "/v1/events/"+eventID.Hex(), testAPIKey,
	)

	if recorder.Code != http.StatusOK {

		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())

	}

	var response eventInspectionResponse

	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {

		t.Fatalf("decode event response: %v", err)

	}

	if response.ID != eventID.Hex() {

		t.Errorf("expected event ID %s, got %s", eventID.Hex(), response.ID)

	}

	if response.Source != "integration-test" {

		t.Errorf("unexpected source: %s", response.Source)

	}

	if response.EventType != "invoice.paid" {

		t.Errorf("unexpected event type: %s", response.EventType)

	}

	if response.DeliveryStatus != "delivered" {

		t.Errorf("unexpected delivery status: %s", response.DeliveryStatus)

	}

	if response.Data["invoice_id"] != "inv-integration-001" {

		t.Errorf("unexpected event data: %#v", response.Data)

	}

}

func TestInspectionIntegrationGetEventNotFound(t *testing.T) {

	_, handler := newMongoInspectionIntegration(t)

	missingID := bson.NewObjectID()

	recorder := serveInspectionIntegrationRequest(

		t, handler, "/v1/events/"+missingID.Hex(), testAPIKey,
	)

	if recorder.Code != http.StatusNotFound {

		t.Fatalf("expected 404, got %d: %s", recorder.Code, recorder.Body.String())

	}

}

func TestInspectionIntegrationGetEventInvalidID(t *testing.T) {

	_, handler := newMongoInspectionIntegration(t)

	recorder := serveInspectionIntegrationRequest(

		t, handler, "/v1/events/not-an-object-id", testAPIKey,
	)

	if recorder.Code != http.StatusBadRequest {

		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())

	}

}

func TestInspectionIntegrationUnauthorized(t *testing.T) {

	_, handler := newMongoInspectionIntegration(t)

	eventID := bson.NewObjectID()

	recorder := serveInspectionIntegrationRequest(

		t, handler, "/v1/events/"+eventID.Hex(), "",
	)

	if recorder.Code != http.StatusUnauthorized {

		t.Fatalf("expected 401, got %d: %s", recorder.Code, recorder.Body.String())

	}

}

func TestInspectionIntegrationAttemptsPagination(t *testing.T) {

	db, handler := newMongoInspectionIntegration(t)

	eventID := bson.NewObjectID()

	insertIntegrationEvent(t, db, eventID)

	insertIntegrationAttempts(t, db, eventID, 5)

	// Fetch the first page.

	first := serveInspectionIntegrationRequest(

		t, handler, "/v1/events/"+eventID.Hex()+"/attempts?limit=2", testAPIKey,
	)

	if first.Code != http.StatusOK {

		t.Fatalf("first page: expected 200, got %d: %s", first.Code, first.Body.String())

	}

	var page1 attemptHistoryResponse

	if err := json.Unmarshal(first.Body.Bytes(), &page1); err != nil {

		t.Fatalf("decode first page: %v", err)

	}

	if len(page1.Attempts) != 2 {

		t.Fatalf("first page: expected 2 attempts, got %d", len(page1.Attempts))

	}

	if !page1.HasMore {

		t.Fatal("first page: expected has_more=true")

	}

	if page1.NextCursor == "" {

		t.Fatal("first page: expected next_cursor")

	}

	seen := make(map[string]bool)

	for _, attempt := range page1.Attempts {

		seen[attempt.ID] = true

	}

	// Fetch subsequent pages using the returned cursor.

	cursor := page1.NextCursor

	for pageNumber := 2; pageNumber <= 3; pageNumber++ {

		path := fmt.Sprintf(

			"/v1/events/%s/attempts?limit=2&cursor=%s",

			eventID.Hex(),

			cursor,
		)

		recorder := serveInspectionIntegrationRequest(t, handler, path, testAPIKey)

		if recorder.Code != http.StatusOK {

			t.Fatalf("page %d: expected 200, got %d: %s",

				pageNumber, recorder.Code, recorder.Body.String())

		}

		var page attemptHistoryResponse

		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {

			t.Fatalf("decode page %d: %v", pageNumber, err)

		}

		for _, attempt := range page.Attempts {

			if seen[attempt.ID] {

				t.Fatalf("duplicate attempt across pages: %s", attempt.ID)

			}

			seen[attempt.ID] = true

		}

		if pageNumber == 2 {

			if len(page.Attempts) != 2 {

				t.Fatalf("second page: expected 2 attempts, got %d", len(page.Attempts))

			}

			if !page.HasMore || page.NextCursor == "" {

				t.Fatal("second page: expected another cursor")

			}

			cursor = page.NextCursor

		} else {

			if len(page.Attempts) != 1 {

				t.Fatalf("third page: expected 1 attempt, got %d", len(page.Attempts))

			}

			if page.HasMore {

				t.Fatal("third page: expected has_more=false")

			}

		}

	}

	if len(seen) != 5 {

		t.Fatalf("expected 5 unique attempts across pages, got %d", len(seen))

	}

}

func TestInspectionIntegrationAttemptsMissingEvent(t *testing.T) {

	_, handler := newMongoInspectionIntegration(t)

	missingID := bson.NewObjectID()

	recorder := serveInspectionIntegrationRequest(

		t, handler, "/v1/events/"+missingID.Hex()+"/attempts", testAPIKey,
	)

	if recorder.Code != http.StatusNotFound {

		t.Fatalf("expected 404, got %d: %s", recorder.Code, recorder.Body.String())

	}

}

func TestInspectionIntegrationAttemptsInvalidCursor(t *testing.T) {

	_, handler := newMongoInspectionIntegration(t)

	eventID := bson.NewObjectID()

	recorder := serveInspectionIntegrationRequest(

		t,

		handler,

		"/v1/events/"+eventID.Hex()+"/attempts?cursor=invalid!",

		testAPIKey,
	)

	if recorder.Code != http.StatusBadRequest {

		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())

	}

}

func TestInspectionIntegrationAttemptsDoNotLeakInternalErrors(t *testing.T) {
	db, _ := newMongoInspectionIntegration(t)
	eventID := bson.NewObjectID()
	insertIntegrationEvent(t, db, eventID)

	// Use a separate client and disconnect it to deterministically force the
	// attempts query to fail without modifying or dropping a live collection.
	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	failureClient, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-inspection-failure-test").
		SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("connect failure-test MongoDB client: %v", err)
	}

	failureDB := failureClient.Database(db.Name())
	failureHandler := NewInspectionHandler(
		db.Collection("events"),
		failureDB.Collection("delivery_attempts"),
		failureDB.Collection("deliveries"),
		testAPIKey,
	)

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := failureClient.Disconnect(cleanupCtx); err != nil {
		cleanupCancel()
		t.Fatalf("disconnect failure-test MongoDB client: %v", err)
	}
	cleanupCancel()

	recorder := serveInspectionIntegrationRequest(
		t, failureHandler, "/v1/events/"+eventID.Hex()+"/attempts", testAPIKey,
	)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", recorder.Code, recorder.Body.String())
	}

	body := strings.ToLower(recorder.Body.String())
	for _, internalDetail := range []string{"mongodb", "database", "connection string", "server selection"} {
		if strings.Contains(body, internalDetail) {
			t.Fatalf("response leaked internal error details: %s", recorder.Body.String())
		}
	}
}
func TestInspectionIntegrationEventDeliveries(t *testing.T) {
	db, handler := newMongoInspectionIntegration(t)

	eventID := bson.NewObjectID()
	insertIntegrationEvent(t, db, eventID)

	endpointA := bson.NewObjectID()
	endpointB := bson.NewObjectID()
	now := time.Now().UTC()

	docs := []any{
		bson.M{
			"_id":          bson.NewObjectID(),
			"event_id":     eventID,
			"endpoint_id":  endpointA,
			"status":       "delivered",
			"attempts":     1,
			"max_attempts": 3,
			"delivered_at": now,
			"created_at":   now,
			"updated_at":   now,
		},
		bson.M{
			"_id":          bson.NewObjectID(),
			"event_id":     eventID,
			"endpoint_id":  endpointB,
			"status":       "pending",
			"attempts":     2,
			"max_attempts": 3,
			"last_error":   "connection refused",
			"created_at":   now,
			"updated_at":   now,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.Collection("deliveries").InsertMany(ctx, docs); err != nil {
		t.Fatalf("insert deliveries: %v", err)
	}

	recorder := serveInspectionIntegrationRequest(
		t,
		handler,
		"/v1/events/"+eventID.Hex()+"/deliveries",
		testAPIKey,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var resp eventDeliveriesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Deliveries) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(resp.Deliveries))
	}
	if resp.EventID != eventID.Hex() {
		t.Errorf("event_id = %q, want %q", resp.EventID, eventID.Hex())
	}

	statuses := map[string]string{}
	for _, d := range resp.Deliveries {
		statuses[d.EndpointID] = d.Status
	}
	if statuses[endpointA.Hex()] != "delivered" {
		t.Errorf(
			"endpointA status = %q, want delivered",
			statuses[endpointA.Hex()],
		)
	}
	if statuses[endpointB.Hex()] != "pending" {
		t.Errorf(
			"endpointB status = %q, want pending",
			statuses[endpointB.Hex()],
		)
	}
}
