package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastFailureAt       *time.Time `json:"last_failure_at"`
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

	lastSuccess, lastFailure, err := s.deliveryOutcomes(ctx, endpointID)
	if err != nil {
		return endpointHealth{}, err
	}

	consecutive, err := s.consecutiveFailures(ctx, endpointID, lastSuccess)
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
		}}},
	})
	if err != nil {
		return endpointHealth{}, fmt.Errorf("aggregate attempt counts: %w", err)
	}
	defer cursor.Close(ctx)

	var counts []struct {
		Outcome string `bson:"_id"`
		Count   int64  `bson:"count"`
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
		case attemptOutcomeFailed:
			health.Failures1h += c.Count
		}
	}

	return health, nil
}

func (s *mongoEndpointHealthStore) deliveryOutcomes(
	ctx context.Context,
	endpointID bson.ObjectID,
) (*time.Time, *time.Time, error) {
	cursor, err := s.deliveries.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "endpoint_id", Value: endpointID},
			{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{
				"delivered", "failed",
			}}}},
		}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: nil},
			{Key: "last_success_at", Value: bson.D{{Key: "$max", Value: bson.D{
				{Key: "$cond", Value: bson.A{
					bson.D{{Key: "$eq", Value: bson.A{
						"$status", "delivered",
					}}},
					"$delivered_at",
					nil,
				}},
			}}}},
			{Key: "last_failure_at", Value: bson.D{{Key: "$max", Value: bson.D{
				{Key: "$cond", Value: bson.A{
					bson.D{{Key: "$eq", Value: bson.A{
						"$status", "failed",
					}}},
					"$failed_at",
					nil,
				}},
			}}}},
		}}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("aggregate delivery outcomes: %w", err)
	}
	defer cursor.Close(ctx)

	var outcomes []struct {
		LastSuccessAt *time.Time `bson:"last_success_at"`
		LastFailureAt *time.Time `bson:"last_failure_at"`
	}
	if err := cursor.All(ctx, &outcomes); err != nil {
		return nil, nil, fmt.Errorf("decode delivery outcomes: %w", err)
	}
	if len(outcomes) == 0 {
		return nil, nil, nil
	}

	return outcomes[0].LastSuccessAt, outcomes[0].LastFailureAt, nil
}

func (s *mongoEndpointHealthStore) consecutiveFailures(
	ctx context.Context,
	endpointID bson.ObjectID,
	lastSuccess *time.Time,
) (int64, error) {
	filter := bson.D{
		{Key: "endpoint_id", Value: endpointID},
		{Key: "status", Value: "failed"},
	}
	if lastSuccess != nil {
		filter = append(filter, bson.E{
			Key:   "failed_at",
			Value: bson.D{{Key: "$gt", Value: *lastSuccess}},
		})
	}

	count, err := s.deliveries.CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("count consecutive delivery failures: %w", err)
	}

	return count, nil
}
