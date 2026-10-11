package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	defaultEventListLimit = 50
	maxEventListLimit     = 100
)

var validDeliveryStatuses = map[string]struct{}{
	"pending":    {},
	"processing": {},
	"delivered":  {},
	"failed":     {},
	"disabled":   {},
}

type eventListCursor struct {
	ReceivedAt time.Time     `json:"received_at"`
	ID         bson.ObjectID `json:"id"`
}

type eventListResponse struct {
	Events     []eventInspectionResponse `json:"events"`
	Limit      int                       `json:"limit"`
	HasMore    bool                      `json:"has_more"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type EventsListHandler struct {
	collection *mongo.Collection
	apiKey     string
}

func NewEventsListHandler(collection *mongo.Collection, apiKey string) *EventsListHandler {
	return &EventsListHandler{
		collection: collection,
		apiKey:     apiKey,
	}
}

func (h *EventsListHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/events", h.list)
}

func (h *EventsListHandler) authorized(w http.ResponseWriter, r *http.Request) bool {
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

func parseEventListLimit(r *http.Request) (int, error) {
	values := r.URL.Query()["limit"]
	if len(values) == 0 {
		return defaultEventListLimit, nil
	}
	if len(values) != 1 {
		return 0, errors.New("limit must be specified once")
	}

	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > maxEventListLimit {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return limit, nil
}

func decodeEventListCursor(value string) (*eventListCursor, error) {
	if value == "" {
		return nil, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}

	var cursor eventListCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, err
	}
	if cursor.ReceivedAt.IsZero() || cursor.ID.IsZero() {
		return nil, errors.New("cursor is missing required fields")
	}
	return &cursor, nil
}

func encodeEventListCursor(cursor eventListCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (h *EventsListHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	query := r.URL.Query()

	statuses := query["delivery_status"]
	if len(statuses) > 1 {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "delivery_status must be specified once",
		})
		return
	}

	status := ""
	if len(statuses) == 1 {
		status = strings.TrimSpace(statuses[0])
		if _, ok := validDeliveryStatuses[status]; !ok {
			writeEventJSON(w, http.StatusBadRequest, map[string]any{
				"error": "invalid delivery_status",
			})
			return
		}
	}

	limit, err := parseEventListLimit(r)
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}

	cursor, err := decodeEventListCursor(query.Get("cursor"))
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid pagination cursor",
		})
		return
	}

	filter := bson.M{}
	if status != "" {
		filter["delivery_status"] = status
	}
	if cursor != nil {
		filter["$or"] = bson.A{
			bson.M{"received_at": bson.M{"$lt": cursor.ReceivedAt}},
			bson.M{
				"received_at": cursor.ReceivedAt,
				"_id":         bson.M{"$lt": cursor.ID},
			},
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectionTimeout)
	defer cancel()

	findOptions := options.Find().
		SetSort(bson.D{
			{Key: "received_at", Value: -1},
			{Key: "_id", Value: -1},
		}).
		SetLimit(int64(limit + 1))

	rows, err := h.collection.Find(ctx, filter, findOptions)
	if err != nil {
		log.Printf("event listing query failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "event listing temporarily unavailable",
		})
		return
	}
	defer func() {
		if err := rows.Close(ctx); err != nil {
			log.Printf("close event listing cursor: %v", err)
		}
	}()

	records := make([]eventInspectionRecord, 0, limit+1)
	for rows.Next(ctx) {
		var event eventInspectionRecord
		if err := rows.Decode(&event); err != nil {
			log.Printf("decode event listing record failed: %v", err)
			writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "event listing temporarily unavailable",
			})
			return
		}
		records = append(records, event)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate event listing results failed: %v", err)
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "event listing temporarily unavailable",
		})
		return
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	response := eventListResponse{
		Events:  make([]eventInspectionResponse, 0, len(records)),
		Limit:   limit,
		HasMore: hasMore,
	}

	for _, event := range records {
		response.Events = append(response.Events, eventInspectionResponse{
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

	if hasMore && len(records) > 0 {
		last := records[len(records)-1]
		response.NextCursor, err = encodeEventListCursor(eventListCursor{
			ReceivedAt: last.ReceivedAt,
			ID:         last.ID,
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
