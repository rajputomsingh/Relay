package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rajputomsingh/relay/internal/lifecycle"
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

	if h.deliveries == nil || h.aggregator == nil {
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "webhook delivery is disabled",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	now := time.Now().UTC()

	// Step 1: atomically claim the replay operation. The filter requires:
	//   - delivery_status == "failed" (the only eligible state), AND
	//   - no active replay claim (replay_in_flight is absent/false), OR
	//   - the previous claim is stale (older than 30 seconds).
	//
	// This is a compare-and-swap on replay_in_flight. Two concurrent
	// requests → only one matches; the other sees ErrNoDocuments and
	// returns 409. A crashed replay whose claim is never released
	// times out after 30s.
	var parent bson.M
	err = h.collection.FindOneAndUpdate(
		ctx,
		bson.M{
			"_id":             objectID,
			"delivery_status": lifecycle.StatusFailed,
			"$or": []bson.M{
				{"replay_in_flight": bson.M{"$exists": false}},
				{"replay_in_flight": false},
				{"replay_claimed_at": bson.M{"$lte": now.Add(-30 * time.Second)}},
			},
		},
		bson.M{
			"$set": bson.M{
				"last_replayed_at":  now,
				"replay_in_flight":  true,
				"replay_claimed_at": now,
			},
			"$inc": bson.M{"replay_count": 1},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&parent)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeEventJSON(w, http.StatusConflict, map[string]any{
				"error": "event not found, delivery status is not failed, or replay already in progress",
			})
			return
		}
		writeEventJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to claim replay",
		})
		return
	}

	// Release the claim on every exit path past this point, whether the
	// requeue succeeds or fails. If the process crashes between claim and
	// release, the 30-second stale timeout in the filter recovers it.
	releaseClaim := func() {
		releaseCtx, releaseCancel := context.WithTimeout(
			context.Background(), 3*time.Second,
		)
		defer releaseCancel()
		_, _ = h.collection.UpdateOne(
			releaseCtx,
			bson.M{
				"_id":               objectID,
				"replay_in_flight":  true,
				"replay_claimed_at": now,
			},
			bson.M{"$unset": bson.M{
				"replay_in_flight":  "",
				"replay_claimed_at": "",
			}},
		)
	}

	currentCycle := 0
	switch v := parent["replay_count"].(type) {
	case int32:
		currentCycle = int(v)
	case int64:
		currentCycle = int(v)
	case int:
		currentCycle = v
	}

	// Step 2: requeue failed children only.
	result, err := h.deliveries.UpdateMany(
		ctx,
		bson.M{
			"event_id": objectID,
			"status":   "failed",
		},
		bson.M{
			"$set": bson.M{
				"status":       "pending",
				"attempts":     0,
				"replay_cycle": currentCycle,
				"updated_at":   now,
			},
			"$unset": bson.M{
				"failed_at":       "",
				"delivered_at":    "",
				"last_error":      "",
				"lease_until":     "",
				"lease_token":     "",
				"next_attempt_at": "",
			},
		},
	)
	if err != nil {
		releaseClaim()
		writeEventJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to requeue deliveries",
		})
		return
	}

	if result.MatchedCount == 0 {
		releaseClaim()
		writeEventJSON(w, http.StatusConflict, map[string]any{
			"error": "event has no failed deliveries to replay",
		})
		return
	}

	// Step 3: aggregator recomputes parent state from child deliveries.
	// Best-effort: failure here does not fail replay; the reconciler
	// repairs stale parents on its next pass.
	_ = h.aggregator.AggregateEvent(ctx, objectID)

	// Release the claim now that the requeue and aggregation are done.
	releaseClaim()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":              "replay_queued",
		"event_id":            objectID.Hex(),
		"idempotency_key":     parent["idempotency_key"],
		"deliveries_requeued": result.ModifiedCount,
	})
}
