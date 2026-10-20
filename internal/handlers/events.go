package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	
)

const maxEventBody = 1 << 20

// FanoutService creates per-endpoint deliveries for accepted events.
type FanoutService interface {
	CreateDeliveries(
		ctx context.Context,
		eventID bson.ObjectID,
		source string,
		eventType string,
	) error
}

type EventsHandler struct {
	collection *mongo.Collection
	apiKey     string
	fanout     FanoutService
}

type createEventRequest struct {
	Source    string         `json:"source"`
	EventType string         `json:"event_type"`
	Data      map[string]any `json:"data"`
}

func NewEventsHandler(
	collection *mongo.Collection,
	apiKey string,
	fanout FanoutService,
) *EventsHandler {
	return &EventsHandler{
		collection: collection,
		apiKey:     apiKey,
		fanout:     fanout,
	}
}

func (h *EventsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/events", h.create)
	mux.HandleFunc("/v1/events/", h.replay)
}

// create godoc
// @Summary      Submit an event
// @Description  Ingests an event and enqueues it for delivery.
// @Tags         events
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        Idempotency-Key header string true "Client-generated idempotency key"
// @Param        event body createEventRequest true "Event payload"
// @Success      202  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Router       /v1/events [post]
func (h *EventsHandler) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeEventJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "method not allowed",
		})
		return
	}

	auth := r.Header.Get("Authorization")
	providedKey := ""

	if strings.HasPrefix(auth, "Bearer ") {
		providedKey = strings.TrimPrefix(auth, "Bearer ")
	}

	if providedKey == "" ||
		subtle.ConstantTimeCompare(
			[]byte(providedKey),
			[]byte(h.apiKey),
		) != 1 {
		writeEventJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "invalid or missing API key",
		})
		return
	}

	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) == 0 || len(key) > 128 {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "Idempotency-Key must contain 1 to 128 characters",
		})
		return
	}

	if r.ContentLength > maxEventBody {
		writeEventJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": "request body exceeds 1 MiB",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEventBody)
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var req createEventRequest
	if err := decoder.Decode(&req); err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid JSON event payload",
		})
		return
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "request must contain exactly one JSON object",
		})
		return
	}

	req.Source = strings.TrimSpace(req.Source)
	req.EventType = strings.TrimSpace(req.EventType)

	if req.Source == "" || len(req.Source) > 200 {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "source is required and must be at most 200 characters",
		})
		return
	}

	if req.EventType == "" || len(req.EventType) > 200 {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "event_type is required and must be at most 200 characters",
		})
		return
	}

	if req.Data == nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "data must be a JSON object",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	deliveryStatus := "disabled"
	if h.fanout != nil {
		deliveryStatus = "managed"
	}

	now := time.Now().UTC()

	doc := bson.M{
		"source":            req.Source,
		"event_type":        req.EventType,
		"data":              req.Data,
		"idempotency_key":   key,
		"received_at":       now,
		"status":            "accepted",
		"delivery_status":   deliveryStatus,
		"delivery_attempts": 0,
	}

	result, err := h.collection.InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeEventJSON(w, http.StatusOK, map[string]any{
				"status":          "already_accepted",
				"idempotency_key": key,
			})
			return
		}

		writeEventJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to persist event",
		})
		return
	}

	eventID, ok := result.InsertedID.(bson.ObjectID)
	if !ok {
		writeEventJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to persist event",
		})
		return
	}

	if h.fanout != nil {
		if err := h.fanout.CreateDeliveries(
			ctx,
			eventID,
			req.Source,
			req.EventType,
		); err != nil {
			writeEventJSON(w, http.StatusInternalServerError, map[string]any{
				"error":    "event accepted but fan-out failed",
				"event_id": eventID.Hex(),
			})
			return
		}
	}

	writeEventJSON(w, http.StatusAccepted, map[string]any{
		"status":          "accepted",
		"event_id":        result.InsertedID,
		"idempotency_key": key,
	})
}

func writeEventJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
