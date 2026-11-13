package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const endpointHealthTimeout = 5 * time.Second

const (
	attemptOutcomeDelivered = "delivered"
	attemptOutcomeFailed    = "failed"
)

// endpointHealth holds the raw delivery statistics for one endpoint.
type endpointHealth struct {
	Attempts1h          int64
	Successes1h         int64
	Failures1h          int64
	ConsecutiveFailures int64
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	P50LatencyMS        *float64
	P95LatencyMS        *float64
	P99LatencyMS        *float64
}

type endpointHealthStore interface {
	Health(context.Context, bson.ObjectID, time.Time) (endpointHealth, error)
}

// endpointHealthResponse is the API representation of endpoint health.
type endpointHealthResponse struct {
	SuccessRate1h       float64    `json:"success_rate_1h"`
	Attempts1h          int64      `json:"attempts_1h"`
	Successes1h         int64      `json:"successes_1h"`
	Failures1h          int64      `json:"failures_1h"`
	ConsecutiveFailures int64      `json:"consecutive_failures"`
	LastSuccessAt       *time.Time `json:"last_success_at" extensions:"x-nullable"`
	LastFailureAt       *time.Time `json:"last_failure_at" extensions:"x-nullable"`
	// Approximate 50th percentile of successful attempt latencies in milliseconds over the past hour; null when no valid samples exist.
	P50LatencyMS *float64 `json:"p50_latency_ms" extensions:"x-nullable" example:"42"`
	// Approximate 95th percentile of successful attempt latencies in milliseconds over the past hour; null when no valid samples exist.
	P95LatencyMS *float64 `json:"p95_latency_ms" extensions:"x-nullable" example:"187"`
	// Approximate 99th percentile of successful attempt latencies in milliseconds over the past hour; null when no valid samples exist.
	P99LatencyMS *float64 `json:"p99_latency_ms" extensions:"x-nullable" example:"512"`
}

// EndpointHealthHandler exposes per-endpoint delivery health over HTTP.
type EndpointHealthHandler struct {
	endpoints endpointsStore
	health    endpointHealthStore
	apiKey    string
	now       func() time.Time
}

// NewEndpointHealthHandler creates an EndpointHealthHandler backed by
// the deliveries and delivery_attempts collections.
func NewEndpointHealthHandler(
	endpointsStore endpointsStore,
	deliveriesCollection *mongo.Collection,
	attemptsCollection *mongo.Collection,
	apiKey string,
) *EndpointHealthHandler {
	return &EndpointHealthHandler{
		endpoints: endpointsStore,
		health: &mongoEndpointHealthStore{
			deliveries: deliveriesCollection,
			attempts:   attemptsCollection,
		},
		apiKey: apiKey,
	}
}

// Register wires the handler into the mux.
func (h *EndpointHealthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/endpoints/{id}/health", h.serve)
}

// serve returns delivery health statistics for a single endpoint.
//
// @Summary      Get endpoint health
// @Description  Returns delivery attempts, success rate, consecutive failures, and the latest success/failure timestamps for an endpoint.
// @Description  Latency percentiles are approximate and use successful attempts from the same one-hour window. Attempts without a finished_at timestamp still count as successes but contribute no latency sample. All three percentiles are null when no valid samples exist; a measured zero is 0. Requires MongoDB 7.0+.
// @Tags         endpoints
// @Produce      json
// @Security     ApiKeyAuth
// @Param        id path string true "Endpoint ID"
// @Success      200 {object} endpointHealthResponse
// @Failure      400 {object} map[string]string
// @Failure      401 {object} map[string]string
// @Failure      404 {object} map[string]string
// @Failure      503 {object} map[string]string
// @Router       /v1/endpoints/{id}/health [get]
func (h *EndpointHealthHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, h.apiKey) {
		return
	}

	id, ok := parseEndpointID(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), endpointHealthTimeout)
	defer cancel()

	if _, err := h.endpoints.Get(ctx, id); err != nil {
		writeEndpointStoreError(w, err, "get")
		return
	}

	now := time.Now().UTC()
	if h.now != nil {
		now = h.now().UTC()
	}

	health, err := h.health.Health(ctx, id, now)
	if err != nil {
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "endpoint health temporarily unavailable",
		})
		return
	}

	writeEventJSON(w, http.StatusOK, toEndpointHealthResponse(health))
}

func toEndpointHealthResponse(health endpointHealth) endpointHealthResponse {
	resp := endpointHealthResponse{
		Attempts1h:          health.Attempts1h,
		Successes1h:         health.Successes1h,
		Failures1h:          health.Failures1h,
		ConsecutiveFailures: health.ConsecutiveFailures,
		LastSuccessAt:       health.LastSuccessAt,
		LastFailureAt:       health.LastFailureAt,
		P50LatencyMS:        health.P50LatencyMS,
		P95LatencyMS:        health.P95LatencyMS,
		P99LatencyMS:        health.P99LatencyMS,
	}

	if health.Attempts1h > 0 {
		resp.SuccessRate1h = float64(health.Successes1h) / float64(health.Attempts1h) * 100
	}

	return resp
}

type mongoEndpointHealthStore struct {
	deliveries *mongo.Collection
	attempts   *mongo.Collection
}

func (s *mongoEndpointHealthStore) Health(
	ctx context.Context,
	endpointID bson.ObjectID,
	now time.Time,
) (endpointHealth, error) {
	health, err := s.attemptCounts(ctx, endpointID, now.Add(-time.Hour))
	if err != nil {
		return endpointHealth{}, err
	}

	lastSuccess, lastFailure, consecutive, err := s.deliveryHistory(ctx, endpointID)
	if err != nil {
		return endpointHealth{}, err
	}

	health.LastSuccessAt = lastSuccess
	health.LastFailureAt = lastFailure
	health.ConsecutiveFailures = consecutive

	return health, nil
}

func (s *mongoEndpointHealthStore) attemptCounts(
	ctx context.Context,
	endpointID bson.ObjectID,
	cutoff time.Time,
) (endpointHealth, error) {
	cursor, err := s.attempts.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "endpoint_id", Value: endpointID},
			{Key: "started_at", Value: bson.D{{Key: "$gte", Value: cutoff}}},
		}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$outcome"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
			// Keep every outcome in the counts, but only delivered attempts
			// contribute latency samples. Missing timestamps produce null,
			// which $percentile ignores rather than treating as zero.
			{Key: "latencies", Value: bson.D{{Key: "$percentile", Value: bson.D{
				{Key: "input", Value: bson.D{{Key: "$cond", Value: bson.A{
					bson.D{{Key: "$eq", Value: bson.A{"$outcome", attemptOutcomeDelivered}}},
					bson.D{{Key: "$subtract", Value: bson.A{"$finished_at", "$started_at"}}},
					nil,
				}}}},
				{Key: "p", Value: bson.A{0.5, 0.95, 0.99}},
				{Key: "method", Value: "approximate"},
			}}}},
		}}},
	})
	if err != nil {
		return endpointHealth{}, fmt.Errorf("aggregate attempt counts: %w", err)
	}
	defer cursor.Close(ctx)

	var counts []struct {
		Outcome string `bson:"_id"`
		Count   int64  `bson:"count"`
		// Nullable elements preserve the difference between no sample and 0ms.
		Latencies []*float64 `bson:"latencies"`
	}
	if err := cursor.All(ctx, &counts); err != nil {
		return endpointHealth{}, fmt.Errorf("decode attempt counts: %w", err)
	}

	var health endpointHealth
	for _, c := range counts {
		health.Attempts1h += c.Count
		switch c.Outcome {
		case attemptOutcomeDelivered:
			health.Successes1h += c.Count
			if len(c.Latencies) != 3 {
				return endpointHealth{}, fmt.Errorf("expected 3 latency percentiles, got %d", len(c.Latencies))
			}
			health.P50LatencyMS = c.Latencies[0]
			health.P95LatencyMS = c.Latencies[1]
			health.P99LatencyMS = c.Latencies[2]
		case attemptOutcomeFailed:
			health.Failures1h += c.Count
		}
	}

	return health, nil
}

func (s *mongoEndpointHealthStore) deliveryHistory(
	ctx context.Context,
	endpointID bson.ObjectID,
) (*time.Time, *time.Time, int64, error) {
	cursor, err := s.deliveries.Find(
		ctx,
		bson.D{
			{Key: "endpoint_id", Value: endpointID},
			{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{
				attemptOutcomeDelivered, attemptOutcomeFailed,
			}}}},
		},
		options.Find().
			SetSort(bson.D{
				{Key: "updated_at", Value: -1},
				{Key: "_id", Value: -1},
			}).
			SetProjection(bson.D{
				{Key: "status", Value: 1},
				{Key: "delivered_at", Value: 1},
				{Key: "failed_at", Value: 1},
				{Key: "updated_at", Value: 1},
			}),
	)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("find delivery history: %w", err)
	}
	defer cursor.Close(ctx)

	var lastSuccess, lastFailure *time.Time
	var consecutive int64
	streakOpen := true

	for cursor.Next(ctx) {
		var doc struct {
			Status      string     `bson:"status"`
			DeliveredAt *time.Time `bson:"delivered_at"`
			FailedAt    *time.Time `bson:"failed_at"`
			UpdatedAt   time.Time  `bson:"updated_at"`
		}
		if err := cursor.Decode(&doc); err != nil {
			return nil, nil, 0, fmt.Errorf("decode delivery history: %w", err)
		}

		switch doc.Status {
		case attemptOutcomeFailed:
			if streakOpen {
				consecutive++
			}
			if lastFailure == nil {
				ts := doc.UpdatedAt
				if doc.FailedAt != nil {
					ts = *doc.FailedAt
				}
				lastFailure = &ts
			}
		case attemptOutcomeDelivered:
			streakOpen = false
			if lastSuccess == nil {
				ts := doc.UpdatedAt
				if doc.DeliveredAt != nil {
					ts = *doc.DeliveredAt
				}
				lastSuccess = &ts
			}
		}

		if lastSuccess != nil && lastFailure != nil {
			break
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, nil, 0, fmt.Errorf("iterate delivery history: %w", err)
	}

	return lastSuccess, lastFailure, consecutive, nil
}
