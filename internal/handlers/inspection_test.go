package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeInspectionStore struct {
	event         eventInspectionRecord
	eventErr      error
	attempts      []delivery.Attempt
	attemptsErr   error
	lastLimit     int
	lastCursor    *attemptCursor
	deliveries    []deliveryState
	deliveriesErr error
}

func (f *fakeInspectionStore) GetEvent(
	_ context.Context,
	id bson.ObjectID,
) (eventInspectionRecord, error) {
	if f.eventErr != nil {
		return eventInspectionRecord{}, f.eventErr
	}
	if f.event.ID != id {
		return eventInspectionRecord{}, errInspectionNotFound
	}
	return f.event, nil
}

func (f *fakeInspectionStore) ListAttempts(
	_ context.Context,
	_ bson.ObjectID,
	limit int,
	cursor *attemptCursor,
) ([]delivery.Attempt, error) {
	f.lastLimit = limit
	f.lastCursor = cursor

	if f.attemptsErr != nil {
		return nil, f.attemptsErr
	}

	if len(f.attempts) > limit {
		return f.attempts[:limit], nil
	}

	return f.attempts, nil
}

func (f *fakeInspectionStore) ListDeliveries(
	_ context.Context,
	_ bson.ObjectID,
) ([]deliveryState, error) {
	if f.deliveriesErr != nil {
		return nil, f.deliveriesErr
	}
	return f.deliveries, nil
}

func newInspectionTestHandler(store inspectionStore) *InspectionHandler {
	return &InspectionHandler{
		store:  store,
		apiKey: testAPIKey,
	}
}

func newInspectionRequest(
	method string,
	path string,
	apiKey string,
) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return request
}

func decodeInspectionResponse(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	target any,
) {
	t.Helper()

	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
}

func TestInspectionGetEventSuccess(t *testing.T) {
	id := bson.NewObjectID()
	now := time.Now().UTC().Truncate(time.Millisecond)

	store := &fakeInspectionStore{
		event: eventInspectionRecord{
			ID:               id,
			Source:           "billing",
			EventType:        "invoice.paid",
			Data:             map[string]any{"invoice_id": "inv_123"},
			IdempotencyKey:   "invoice-paid-123",
			ReceivedAt:       now,
			Status:           "accepted",
			DeliveryStatus:   "delivered",
			DeliveryAttempts: 1,
		},
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+id.Hex(),
		testAPIKey,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response eventInspectionResponse
	decodeInspectionResponse(t, recorder, &response)

	if response.ID != id.Hex() {
		t.Errorf("unexpected event ID: %s", response.ID)
	}
	if response.EventType != "invoice.paid" {
		t.Errorf("unexpected event type: %s", response.EventType)
	}
	if response.DeliveryStatus != "delivered" {
		t.Errorf("unexpected delivery status: %s", response.DeliveryStatus)
	}
}

func TestInspectionGetEventUnauthorized(t *testing.T) {
	id := bson.NewObjectID()
	store := &fakeInspectionStore{
		event: eventInspectionRecord{ID: id},
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+id.Hex(),
		"",
	))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestInspectionGetEventInvalidID(t *testing.T) {
	handler := newInspectionTestHandler(&fakeInspectionStore{})
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/not-an-object-id",
		testAPIKey,
	))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}

func TestInspectionGetEventNotFound(t *testing.T) {
	id := bson.NewObjectID()
	handler := newInspectionTestHandler(&fakeInspectionStore{})
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+id.Hex(),
		testAPIKey,
	))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestInspectionGetEventStorageFailure(t *testing.T) {
	id := bson.NewObjectID()
	store := &fakeInspectionStore{
		eventErr: errors.New("database connection details"),
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+id.Hex(),
		testAPIKey,
	))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "database connection details") {
		t.Fatal("response leaked internal database error")
	}
}

func TestInspectionAttemptsPagination(t *testing.T) {
	eventID := bson.NewObjectID()
	now := time.Now().UTC()

	store := &fakeInspectionStore{
		event: eventInspectionRecord{ID: eventID},
		attempts: []delivery.Attempt{
			{
				ID:            bson.NewObjectID(),
				EventID:       eventID,
				AttemptNumber: 3,
				StartedAt:     now,
				Outcome:       "failed",
			},
			{
				ID:            bson.NewObjectID(),
				EventID:       eventID,
				AttemptNumber: 2,
				StartedAt:     now.Add(-time.Minute),
				Outcome:       "failed",
			},
			{
				ID:            bson.NewObjectID(),
				EventID:       eventID,
				AttemptNumber: 1,
				StartedAt:     now.Add(-2 * time.Minute),
				Outcome:       "success",
			},
		},
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/attempts?limit=2",
		testAPIKey,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response attemptHistoryResponse
	decodeInspectionResponse(t, recorder, &response)

	if len(response.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(response.Attempts))
	}
	if !response.HasMore {
		t.Fatal("expected has_more=true")
	}
	if response.NextCursor == "" {
		t.Fatal("expected a next cursor")
	}
	if store.lastLimit != 3 {
		t.Fatalf("expected store limit 3, got %d", store.lastLimit)
	}
}

func TestInspectionAttemptsInvalidLimit(t *testing.T) {
	eventID := bson.NewObjectID()
	store := &fakeInspectionStore{
		event: eventInspectionRecord{ID: eventID},
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/attempts?limit=101",
		testAPIKey,
	))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}

func TestInspectionAttemptsInvalidCursor(t *testing.T) {
	eventID := bson.NewObjectID()
	store := &fakeInspectionStore{
		event: eventInspectionRecord{ID: eventID},
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/attempts?cursor=invalid!",
		testAPIKey,
	))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}

func TestInspectionAttemptsNotFound(t *testing.T) {
	eventID := bson.NewObjectID()
	handler := newInspectionTestHandler(&fakeInspectionStore{})
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/attempts",
		testAPIKey,
	))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestInspectionAttemptsStorageFailure(t *testing.T) {
	eventID := bson.NewObjectID()
	store := &fakeInspectionStore{
		event:       eventInspectionRecord{ID: eventID},
		attemptsErr: errors.New("database unavailable"),
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/attempts",
		testAPIKey,
	))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recorder.Code)
	}
}

func TestInspectionDeliveriesSuccess(t *testing.T) {
	eventID := bson.NewObjectID()
	endpointA := bson.NewObjectID()
	endpointB := bson.NewObjectID()
	now := time.Now().UTC()

	store := &fakeInspectionStore{
		event: eventInspectionRecord{ID: eventID},
		deliveries: []deliveryState{
			{
				ID:          bson.NewObjectID(),
				EventID:     eventID,
				EndpointID:  endpointA,
				Status:      "delivered",
				Attempts:    1,
				MaxAttempts: 3,
				DeliveredAt: &now,
				UpdatedAt:   now,
			},
			{
				ID:          bson.NewObjectID(),
				EventID:     eventID,
				EndpointID:  endpointB,
				Status:      "pending",
				Attempts:    1,
				MaxAttempts: 3,
				LastError:   "connection refused",
				UpdatedAt:   now,
			},
		},
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/deliveries",
		testAPIKey,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response eventDeliveriesResponse
	decodeInspectionResponse(t, recorder, &response)

	if response.EventID != eventID.Hex() {
		t.Errorf("event_id = %q; want %q", response.EventID, eventID.Hex())
	}
	if len(response.Deliveries) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(response.Deliveries))
	}
}

func TestInspectionDeliveriesUnauthorized(t *testing.T) {
	eventID := bson.NewObjectID()
	handler := newInspectionTestHandler(&fakeInspectionStore{
		event: eventInspectionRecord{ID: eventID},
	})
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/deliveries",
		"",
	))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestInspectionDeliveriesNotFound(t *testing.T) {
	eventID := bson.NewObjectID()
	handler := newInspectionTestHandler(&fakeInspectionStore{})
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/deliveries",
		testAPIKey,
	))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestInspectionDeliveriesStorageFailure(t *testing.T) {
	eventID := bson.NewObjectID()
	store := &fakeInspectionStore{
		event:         eventInspectionRecord{ID: eventID},
		deliveriesErr: errors.New("database unavailable"),
	}

	handler := newInspectionTestHandler(store)
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, newInspectionRequest(
		http.MethodGet,
		"/v1/events/"+eventID.Hex()+"/deliveries",
		testAPIKey,
	))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recorder.Code)
	}
}
