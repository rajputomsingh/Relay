package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (h *EventsHandler) replay(w http.ResponseWriter, r *http.Request) {
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

	const suffix = "/replay"
	path := strings.TrimPrefix(r.URL.Path, "/v1/events/")
	if !strings.HasSuffix(path, suffix) {
		http.NotFound(w, r)
		return
	}

	id := strings.TrimSuffix(path, suffix)
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	objectID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid event ID",
		})
		return
	}

	if h.fanout == nil {
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "webhook delivery is disabled",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	now := time.Now().UTC()

	var event bson.M
	err = h.collection.FindOneAndUpdate(
		ctx,
		bson.M{
			"_id":             objectID,
			"delivery_status": "failed",
		},
		bson.M{
			"$set": bson.M{
				"delivery_status":   "pending",
				"delivery_attempts": 0,
				"next_attempt_at":   now,
				"last_replayed_at":  now,
			},
			"$inc": bson.M{
				"replay_count": 1,
			},
			"$unset": bson.M{
				"failed_at":           "",
				"delivered_at":        "",
				"last_delivery_error": "",
				"lease_until":         "",
				"lease_token":         "",
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&event)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			writeEventJSON(w, http.StatusConflict, map[string]any{
				"error": "event not found or delivery status is not failed",
			})
			return
		}

		writeEventJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to replay event",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":          "replay_queued",
		"event_id":        objectID.Hex(),
		"idempotency_key": event["idempotency_key"],
		"delivery_status": "pending",
	})
}
