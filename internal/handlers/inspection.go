package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	defaultAttemptLimit = 50
	maxAttemptLimit     = 100
	inspectionTimeout   = 5 * time.Second
)

var errInspectionNotFound = errors.New("event not found")

type inspectionStore interface {
	GetEvent(context.Context, bson.ObjectID) (eventInspectionRecord, error)
	ListAttempts(context.Context, bson.ObjectID, int, *attemptCursor) ([]delivery.Attempt, error)
	ListDeliveries(context.Context, bson.ObjectID) ([]deliveryState, error)
}

type mongoInspectionStore struct {
	events     *mongo.Collection
	attempts   *mongo.Collection
	deliveries *mongo.Collection
}

type InspectionHandler struct {
	store  inspectionStore
	apiKey string
}

type eventInspectionRecord struct {
	ID               bson.ObjectID  `bson:"_id"`
	Source           string         `bson:"source"`
	EventType        string         `bson:"event_type"`
	Data             map[string]any `bson:"data"`
	IdempotencyKey   string         `bson:"idempotency_key"`
	ReceivedAt       time.Time      `bson:"received_at"`
	Status           string         `bson:"status"`
	DeliveryStatus   string         `bson:"delivery_status"`
	DeliveryAttempts int            `bson:"delivery_attempts"`
	DeliveredAt      *time.Time     `bson:"delivered_at,omitempty"`
	FailedAt         *time.Time     `bson:"failed_at,omitempty"`
	ReplayCount      int            `bson:"replay_count,omitempty"`
	LastReplayedAt   *time.Time     `bson:"last_replayed_at,omitempty"`
}

type eventInspectionResponse struct {
	ID               string         `json:"id"`
	Source           string         `json:"source"`
	EventType        string         `json:"event_type"`
	Data             map[string]any `json:"data"`
	IdempotencyKey   string         `json:"idempotency_key"`
	ReceivedAt       time.Time      `json:"received_at"`
	Status           string         `json:"status"`
	DeliveryStatus   string         `json:"delivery_status"`
	DeliveryAttempts int            `json:"delivery_attempts"`
	DeliveredAt      *time.Time     `json:"delivered_at,omitempty"`
	FailedAt         *time.Time     `json:"failed_at,omitempty"`
	ReplayCount      int            `json:"replay_count,omitempty"`
	LastReplayedAt   *time.Time     `json:"last_replayed_at,omitempty"`
}

type attemptCursor struct {
	StartedAt time.Time     `json:"started_at"`
	ID        bson.ObjectID `json:"id"`
}

type attemptHistoryResponse struct {
	EventID    string            `json:"event_id"`
	Attempts   []attemptResponse `json:"attempts"`
	Limit      int               `json:"limit"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type attemptResponse struct {
	ID            string    `json:"id"`
	EventID       string    `json:"event_id"`
	EndpointID    string    `json:"endpoint_id,omitempty"`
	AttemptNumber int       `json:"attempt_number"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMS    int64     `json:"duration_ms"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	Outcome       string    `json:"outcome"`
}

type deliveryState struct {
	ID            bson.ObjectID `bson:"_id"`
	EventID       bson.ObjectID `bson:"event_id"`
	EndpointID    bson.ObjectID `bson:"endpoint_id"`
	Status        string        `bson:"status"`
	Attempts      int           `bson:"attempts"`
	MaxAttempts   int           `bson:"max_attempts"`
	NextAttemptAt *time.Time    `bson:"next_attempt_at,omitempty"`
	DeliveredAt   *time.Time    `bson:"delivered_at,omitempty"`
	FailedAt      *time.Time    `bson:"failed_at,omitempty"`
	LastError     string        `bson:"last_error,omitempty"`
	UpdatedAt     time.Time     `bson:"updated_at"`
}

type deliveryStateResponse struct {
	EndpointID    string     `json:"endpoint_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	FailedAt      *time.Time `json:"failed_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type eventDeliveriesResponse struct {
	EventID    string                  `json:"event_id"`
	Deliveries []deliveryStateResponse `json:"deliveries"`
}

func NewInspectionHandler(
	eventsCollection *mongo.Collection,
	attemptsCollection *mongo.Collection,
	deliveriesCollection *mongo.Collection,
	apiKey string,
) *InspectionHandler {
	return &InspectionHandler{
		store: &mongoInspectionStore{
			events:     eventsCollection,
			attempts:   attemptsCollection,
			deliveries: deliveriesCollection,
		},
		apiKey: apiKey,
	}
}

func (h *InspectionHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/events/{id}", h.getEvent)
	mux.HandleFunc("GET /v1/events/{id}/attempts", h.getAttempts)
	mux.HandleFunc("GET /v1/events/{id}/deliveries", h.getDeliveries)
}

func (h *InspectionHandler) authorized(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "Bearer "

	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) ||
		h.apiKey == "" ||
		subtle.ConstantTimeCompare(
			[]byte(strings.TrimPrefix(header, prefix)),
			[]byte(h.apiKey),
		) != 1 {
		writeEventJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "unauthorized",
		})
		return false
	}

	return true
}

func parseEventID(r *http.Request) (bson.ObjectID, error) {
	return bson.ObjectIDFromHex(r.PathValue("id"))
}

func (h *InspectionHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	id, err := parseEventID(r)
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid event ID",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectionTimeout)
	defer cancel()

	event, err := h.store.GetEvent(ctx, id)
	if errors.Is(err, errInspectionNotFound) {
		writeEventJSON(w, http.StatusNotFound, map[string]any{
			"error": "event not found",
		})
		return
	}
	if err != nil {
		log.Printf("event inspection failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "event inspection temporarily unavailable",
		})
		return
	}

	writeEventJSON(w, http.StatusOK, eventInspectionResponse{
		ID:               event.ID.Hex(),
		Source:           event.Source,
		EventType:        event.EventType,
		Data:             event.Data,
		IdempotencyKey:   event.IdempotencyKey,
		ReceivedAt:       event.ReceivedAt,
		Status:           event.Status,
		DeliveryStatus:   event.DeliveryStatus,
		DeliveryAttempts: event.DeliveryAttempts,
		DeliveredAt:      event.DeliveredAt,
		FailedAt:         event.FailedAt,
		ReplayCount:      event.ReplayCount,
		LastReplayedAt:   event.LastReplayedAt,
	})
}

func (h *InspectionHandler) getAttempts(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	id, err := parseEventID(r)
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid event ID",
		})
		return
	}

	limit, err := parseAttemptLimit(r)
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}

	cursor, err := decodeAttemptCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid pagination cursor",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectionTimeout)
	defer cancel()

	if _, err := h.store.GetEvent(ctx, id); err != nil {
		if errors.Is(err, errInspectionNotFound) {
			writeEventJSON(w, http.StatusNotFound, map[string]any{
				"error": "event not found",
			})
			return
		}

		log.Printf("event lookup for attempt history failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "attempt history temporarily unavailable",
		})
		return
	}

	attempts, err := h.store.ListAttempts(ctx, id, limit+1, cursor)
	if err != nil {
		log.Printf("attempt history query failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "attempt history temporarily unavailable",
		})
		return
	}

	hasMore := len(attempts) > limit
	if hasMore {
		attempts = attempts[:limit]
	}

	response := attemptHistoryResponse{
		EventID:  id.Hex(),
		Attempts: make([]attemptResponse, 0, len(attempts)),
		Limit:    limit,
		HasMore:  hasMore,
	}

	for _, attempt := range attempts {
		resp := attemptResponse{
			ID:            attempt.ID.Hex(),
			EventID:       attempt.EventID.Hex(),
			AttemptNumber: attempt.AttemptNumber,
			StartedAt:     attempt.StartedAt,
			FinishedAt:    attempt.FinishedAt,
			DurationMS:    attempt.DurationMS,
			HTTPStatus:    attempt.HTTPStatus,
			Outcome:       attempt.Outcome,
		}
		if !attempt.EndpointID.IsZero() {
			resp.EndpointID = attempt.EndpointID.Hex()
		}
		response.Attempts = append(response.Attempts, resp)
	}

	if hasMore && len(attempts) > 0 {
		last := attempts[len(attempts)-1]
		response.NextCursor, err = encodeAttemptCursor(attemptCursor{
			StartedAt: last.StartedAt,
			ID:        last.ID,
		})
		if err != nil {
			writeEventJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "failed to construct pagination response",
			})
			return
		}
	}

	writeEventJSON(w, http.StatusOK, response)
}

func (h *InspectionHandler) getDeliveries(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	id, err := parseEventID(r)
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid event ID",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectionTimeout)
	defer cancel()

	if _, err := h.store.GetEvent(ctx, id); err != nil {
		if errors.Is(err, errInspectionNotFound) {
			writeEventJSON(w, http.StatusNotFound, map[string]any{
				"error": "event not found",
			})
			return
		}

		log.Printf("event lookup for deliveries failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "delivery state temporarily unavailable",
		})
		return
	}

	deliveries, err := h.store.ListDeliveries(ctx, id)
	if err != nil {
		log.Printf("delivery state query failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "delivery state temporarily unavailable",
		})
		return
	}

	response := eventDeliveriesResponse{
		EventID:    id.Hex(),
		Deliveries: make([]deliveryStateResponse, 0, len(deliveries)),
	}

	for _, d := range deliveries {
		response.Deliveries = append(
			response.Deliveries,
			deliveryStateResponse{
				EndpointID:    d.EndpointID.Hex(),
				Status:        d.Status,
				Attempts:      d.Attempts,
				MaxAttempts:   d.MaxAttempts,
				NextAttemptAt: d.NextAttemptAt,
				DeliveredAt:   d.DeliveredAt,
				FailedAt:      d.FailedAt,
				LastError:     d.LastError,
				UpdatedAt:     d.UpdatedAt,
			},
		)
	}

	writeEventJSON(w, http.StatusOK, response)
}

func (s *mongoInspectionStore) GetEvent(
	ctx context.Context,
	id bson.ObjectID,
) (eventInspectionRecord, error) {
	var event eventInspectionRecord

	err := s.events.FindOne(ctx, bson.M{"_id": id}).Decode(&event)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return eventInspectionRecord{}, errInspectionNotFound
	}
	if err != nil {
		return eventInspectionRecord{}, fmt.Errorf("find event: %w", err)
	}

	return event, nil
}

func (s *mongoInspectionStore) ListAttempts(
	ctx context.Context,
	eventID bson.ObjectID,
	limit int,
	cursor *attemptCursor,
) ([]delivery.Attempt, error) {
	filter := bson.M{"event_id": eventID}

	if cursor != nil {
		filter["$or"] = bson.A{
			bson.M{"started_at": bson.M{"$lt": cursor.StartedAt}},
			bson.M{
				"started_at": cursor.StartedAt,
				"_id":        bson.M{"$lt": cursor.ID},
			},
		}
	}

	findOptions := options.Find().
		SetSort(bson.D{
			{Key: "started_at", Value: -1},
			{Key: "_id", Value: -1},
		}).
		SetLimit(int64(limit))

	rows, err := s.attempts.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("find attempts: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(ctx); closeErr != nil {
			log.Printf("close attempt cursor: %v", closeErr)
		}
	}()

	result := make([]delivery.Attempt, 0, limit)
	for rows.Next(ctx) {
		var attempt delivery.Attempt
		if err := rows.Decode(&attempt); err != nil {
			return nil, fmt.Errorf("decode attempt: %w", err)
		}
		result = append(result, attempt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempts: %w", err)
	}

	return result, nil
}

func (s *mongoInspectionStore) ListDeliveries(
	ctx context.Context,
	eventID bson.ObjectID,
) ([]deliveryState, error) {
	rows, err := s.deliveries.Find(
		ctx,
		bson.M{"event_id": eventID},
		options.Find().SetSort(
			bson.D{{Key: "updated_at", Value: -1}},
		),
	)
	if err != nil {
		return nil, fmt.Errorf("find deliveries: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(ctx); closeErr != nil {
			log.Printf("close deliveries cursor: %v", closeErr)
		}
	}()

	result := make([]deliveryState, 0)
	for rows.Next(ctx) {
		var d deliveryState
		if err := rows.Decode(&d); err != nil {
			return nil, fmt.Errorf("decode delivery: %w", err)
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deliveries: %w", err)
	}

	return result, nil
}

func parseAttemptLimit(r *http.Request) (int, error) {
	values := r.URL.Query()["limit"]
	if len(values) == 0 {
		return defaultAttemptLimit, nil
	}
	if len(values) != 1 {
		return 0, errors.New("limit must be specified once")
	}

	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > maxAttemptLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxAttemptLimit)
	}

	return limit, nil
}

func encodeAttemptCursor(cursor attemptCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeAttemptCursor(value string) (*attemptCursor, error) {
	if value == "" {
		return nil, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}

	var cursor attemptCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, err
	}
	if cursor.StartedAt.IsZero() || cursor.ID.IsZero() {
		return nil, errors.New("cursor is missing required fields")
	}

	return &cursor, nil
}
