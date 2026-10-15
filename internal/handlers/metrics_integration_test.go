package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func newMongoMetricsIntegration(
	t *testing.T,
) (*mongo.Database, *MetricsHandler) {
	t.Helper()

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run MongoDB metrics integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-metrics-integration-tests").
		SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("connect to test MongoDB: %v", err)
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		disconnectCtx, disconnectCancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer disconnectCancel()
		_ = client.Disconnect(disconnectCtx)

		t.Fatalf("ping test MongoDB: %v", err)
	}

	dbName := fmt.Sprintf("rmt_%x", time.Now().UnixNano())
	db := client.Database(dbName)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()

		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop integration test database: %v", err)
		}
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test MongoDB client: %v", err)
		}
	})

	handler := NewMetricsHandler(
		db.Collection("events"),
		db.Collection("delivery_attempts"),
		db.Collection("deliveries"),
		"metrics-integration-test-key",
	)

	return db, handler
}

func serveMetricsIntegrationRequest(
	t *testing.T,
	handler *MetricsHandler,
) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set(
		"Authorization",
		"Bearer metrics-integration-test-key",
	)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	return recorder
}

// fakeWorkerHealth satisfies workerHealth for integration tests.
type fakeWorkerHealth struct {
	lastProcessedAt time.Time
	errorsTotal     int64
}

func (f *fakeWorkerHealth) LastProcessedAt() time.Time {
	return f.lastProcessedAt
}

func (f *fakeWorkerHealth) ProcessingErrorsTotal() int64 {
	return f.errorsTotal
}

func TestMetricsIntegrationEmptyCollections(t *testing.T) {
	_, handler := newMongoMetricsIntegration(t)

	recorder := serveMetricsIntegrationRequest(t, handler)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d; body: %s",
			recorder.Code,
			http.StatusOK,
			recorder.Body.String(),
		)
	}

	body := recorder.Body.String()

	expected := []string{
		`relay_events{status="pending"} 0`,
		`relay_events{status="processing"} 0`,
		`relay_events{status="delivered"} 0`,
		`relay_events{status="failed"} 0`,
		`relay_events{status="disabled"} 0`,
		`relay_deliveries{status="pending"} 0`,
		`relay_deliveries{status="processing"} 0`,
		`relay_deliveries{status="delivered"} 0`,
		`relay_deliveries{status="failed"} 0`,
		"relay_deliveries_terminal_failures 0",
		"relay_delivery_attempts 0",
		"relay_delivery_retries 0",
		`relay_delivery_duration_seconds_bucket{le="100"} 0`,
		`relay_delivery_duration_seconds_bucket{le="500"} 0`,
		`relay_delivery_duration_seconds_bucket{le="1000"} 0`,
		`relay_delivery_duration_seconds_bucket{le="5000"} 0`,
		`relay_delivery_duration_seconds_bucket{le="10000"} 0`,
		`relay_delivery_duration_seconds_bucket{le="+Inf"} 0`,
		"relay_delivery_duration_seconds_sum 0",
		"relay_delivery_duration_seconds_count 0",
	}

	for _, metric := range expected {
		if !strings.Contains(body, metric) {
			t.Errorf("metrics output missing %q", metric)
		}
	}
}

func TestMetricsIntegrationAggregatesEventsAttemptsAndDurations(
	t *testing.T,
) {
	db, handler := newMongoMetricsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eventStatuses := []string{
		"pending",
		"pending",
		"processing",
		"delivered",
		"delivered",
		"delivered",
		"failed",
		"disabled",
	}

	var events []any
	for _, status := range eventStatuses {
		events = append(events, bson.M{
			"delivery_status": status,
		})
	}

	if _, err := db.Collection("events").InsertMany(ctx, events); err != nil {
		t.Fatalf("insert metrics test events: %v", err)
	}

	durationsMS := []int64{
		50, 100, 101, 500, 501, 1000,
		1001, 5000, 5001, 10000, 10001,
	}

	attemptNumbers := []int{
		1, 1, 2, 1, 2, 1,
		2, 1, 2, 1, 2,
	}

	var attempts []any
	for i, duration := range durationsMS {
		attempts = append(attempts, bson.M{
			"event_id":       bson.NewObjectID(),
			"attempt_number": attemptNumbers[i],
			"duration_ms":    duration,
			"outcome":        "delivered",
		})
	}

	if _, err := db.Collection("delivery_attempts").InsertMany(
		ctx,
		attempts,
	); err != nil {
		t.Fatalf("insert metrics test attempts: %v", err)
	}

	recorder := serveMetricsIntegrationRequest(t, handler)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d; body: %s",
			recorder.Code,
			http.StatusOK,
			recorder.Body.String(),
		)
	}

	body := recorder.Body.String()

	expected := []string{
		`relay_events{status="pending"} 2`,
		`relay_events{status="processing"} 1`,
		`relay_events{status="delivered"} 3`,
		`relay_events{status="failed"} 1`,
		`relay_events{status="disabled"} 1`,
		"relay_delivery_attempts 11",
		"relay_delivery_retries 5",
		`relay_delivery_duration_seconds_bucket{le="100"} 2`,
		`relay_delivery_duration_seconds_bucket{le="500"} 4`,
		`relay_delivery_duration_seconds_bucket{le="1000"} 6`,
		`relay_delivery_duration_seconds_bucket{le="5000"} 8`,
		`relay_delivery_duration_seconds_bucket{le="10000"} 10`,
		`relay_delivery_duration_seconds_bucket{le="+Inf"} 11`,
		"relay_delivery_duration_seconds_sum 33.255",
		"relay_delivery_duration_seconds_count 11",
	}

	for _, metric := range expected {
		if !strings.Contains(body, metric) {
			t.Errorf("metrics output missing %q", metric)
		}
	}
}

func TestMetricsIntegrationDeliveryCounts(t *testing.T) {
	db, handler := newMongoMetricsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eventID := bson.NewObjectID()
	endpointA := bson.NewObjectID()
	endpointB := bson.NewObjectID()
	now := time.Now().UTC()

	oldPendingTime := now.Add(-5 * time.Minute)

	deliveries := []any{
		bson.M{
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
			"event_id":        eventID,
			"endpoint_id":     endpointB,
			"status":          "pending",
			"attempts":        1,
			"max_attempts":    3,
			"next_attempt_at": now.Add(time.Minute),
			"created_at":      oldPendingTime,
			"updated_at":      now,
		},
		bson.M{
			"event_id":     bson.NewObjectID(),
			"endpoint_id":  endpointA,
			"status":       "failed",
			"attempts":     3,
			"max_attempts": 3,
			"failed_at":    now,
			"created_at":   now,
			"updated_at":   now,
		},
		bson.M{
			"event_id":     bson.NewObjectID(),
			"endpoint_id":  endpointA,
			"status":       "processing",
			"attempts":     1,
			"max_attempts": 3,
			"lease_until":  now.Add(time.Minute),
			"created_at":   now,
			"updated_at":   now,
		},
	}

	if _, err := db.Collection("deliveries").InsertMany(
		ctx, deliveries,
	); err != nil {
		t.Fatalf("insert deliveries: %v", err)
	}

	recorder := serveMetricsIntegrationRequest(t, handler)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d; body: %s",
			recorder.Code,
			http.StatusOK,
			recorder.Body.String(),
		)
	}

	body := recorder.Body.String()

	expected := []string{
		`relay_deliveries{status="pending"} 1`,
		`relay_deliveries{status="processing"} 1`,
		`relay_deliveries{status="delivered"} 1`,
		`relay_deliveries{status="failed"} 1`,
		"relay_deliveries_terminal_failures 1",
		fmt.Sprintf(
			`relay_deliveries_per_endpoint{endpoint_id=%q,status="delivered"} 1`,
			endpointA.Hex(),
		),
		fmt.Sprintf(
			`relay_deliveries_per_endpoint{endpoint_id=%q,status="failed"} 1`,
			endpointA.Hex(),
		),
		fmt.Sprintf(
			`relay_deliveries_per_endpoint{endpoint_id=%q,status="pending"} 1`,
			endpointB.Hex(),
		),
		"relay_deliveries_oldest_pending_seconds",
	}

	for _, metric := range expected {
		if !strings.Contains(body, metric) {
			t.Errorf("metrics output missing %q", metric)
		}
	}
}

func TestMetricsIntegrationWorkerHealth(t *testing.T) {
	_, handler := newMongoMetricsIntegration(t)

	health := &fakeWorkerHealth{
		lastProcessedAt: time.Now().UTC(),
		errorsTotal:     3,
	}
	handler.WithWorkerHealth(health)

	recorder := serveMetricsIntegrationRequest(t, handler)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d; body: %s",
			recorder.Code,
			http.StatusOK,
			recorder.Body.String(),
		)
	}

	body := recorder.Body.String()

	expected := []string{
		"relay_worker_last_processed_timestamp_seconds",
		"relay_worker_processing_errors_total 3",
	}

	for _, metric := range expected {
		if !strings.Contains(body, metric) {
			t.Errorf("metrics output missing %q", metric)
		}
	}
}

func TestMetricsIntegrationWorkerHealthAbsentWithoutWorker(t *testing.T) {
	_, handler := newMongoMetricsIntegration(t)

	recorder := serveMetricsIntegrationRequest(t, handler)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := recorder.Body.String()

	// Without a worker attached, worker health metrics must not appear.
	if strings.Contains(body, "relay_worker_last_processed_timestamp_seconds") {
		t.Error("worker timestamp metric appeared without a worker")
	}
	if strings.Contains(body, "relay_worker_processing_errors_total") {
		t.Error("worker errors metric appeared without a worker")
	}
}
