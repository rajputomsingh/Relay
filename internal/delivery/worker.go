package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Config struct {
	WebhookURL     string
	PollInterval   time.Duration
	MaxAttempts    int
	RequestTimeout time.Duration
	LeaseDuration  time.Duration
	Logger         *slog.Logger
}

type Worker struct {
	events   *mongo.Collection
	attempts *mongo.Collection
	config   Config
	logger   *slog.Logger
	client   *http.Client
}

type eventRecord struct {
	ID             bson.ObjectID  `bson:"_id"`
	Source         string         `bson:"source"`
	EventType      string         `bson:"event_type"`
	Data           map[string]any `bson:"data"`
	IdempotencyKey string         `bson:"idempotency_key"`
	ReceivedAt     time.Time      `bson:"received_at"`
	Attempts       int            `bson:"delivery_attempts"`
	LeaseToken     string         `bson:"lease_token"`
}

type webhookPayload struct {
	EventID        string         `json:"event_id"`
	Source         string         `json:"source"`
	EventType      string         `json:"event_type"`
	Data           map[string]any `json:"data"`
	IdempotencyKey string         `json:"idempotency_key"`
	ReceivedAt     time.Time      `json:"received_at"`
}

func NewWorker(
	events *mongo.Collection,
	attempts *mongo.Collection,
	cfg Config,
) *Worker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Worker{
		events:   events,
		attempts: attempts,
		config:   cfg,
		logger:   logger,
		client: &http.Client{
			Timeout: cfg.RequestTimeout,
			CheckRedirect: func(
				req *http.Request,
				via []*http.Request,
			) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Run polls MongoDB until the context is cancelled.
// Start the worker only when a webhook destination is configured.
func (w *Worker) Run(ctx context.Context) {
	if w.config.PollInterval <= 0 {
		w.logger.Error(
			"delivery worker configuration invalid",
			"reason", "poll interval must be positive",
		)
		return
	}

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	w.logger.Info("delivery worker started")

	for {
		if ctx.Err() != nil {
			w.logger.Info("delivery worker stopped")
			return
		}

		if err := w.processOne(ctx); err != nil &&
			!errors.Is(err, mongo.ErrNoDocuments) &&
			ctx.Err() == nil {
			w.logger.Error(
				"delivery worker processing failed",
				"error_type", fmt.Sprintf("%T", err),
			)
		}

		select {
		case <-ctx.Done():
			w.logger.Info("delivery worker stopped")
			return
		case <-ticker.C:
		}
	}
}

// claim atomically assigns one due event to this worker.
// Every claim receives a fresh token, including expired-lease recovery.
// The token fences off state updates from stale workers.
func (w *Worker) claim(ctx context.Context) (eventRecord, error) {
	now := time.Now().UTC()
	leaseToken := bson.NewObjectID().Hex()

	filter := bson.M{
		"$or": []bson.M{
			{
				"delivery_status": "pending",
				"$or": []bson.M{
					{"next_attempt_at": bson.M{"$exists": false}},
					{"next_attempt_at": bson.M{"$lte": now}},
				},
			},
			{
				"delivery_status":   "processing",
				"lease_until":       bson.M{"$lte": now},
				"delivery_attempts": bson.M{"$lt": w.config.MaxAttempts},
			},
		},
	}

	update := bson.M{
		"$set": bson.M{
			"delivery_status": "processing",
			"lease_until":     now.Add(w.config.LeaseDuration),
			"last_attempt_at": now,
			"lease_token":     leaseToken,
		},
		"$inc": bson.M{
			"delivery_attempts": 1,
		},
	}

	var event eventRecord

	err := w.events.FindOneAndUpdate(
		ctx,
		filter,
		update,
		options.FindOneAndUpdate().
			SetSort(bson.D{{Key: "received_at", Value: 1}}).
			SetReturnDocument(options.After),
	).Decode(&event)

	return event, err
}

// failExhaustedLeases permanently fails expired jobs that have
// already used all configured delivery attempts.
func (w *Worker) failExhaustedLeases(ctx context.Context) error {
	now := time.Now().UTC()

	result, err := w.events.UpdateMany(
		ctx,
		bson.M{
			"delivery_status":   "processing",
			"lease_until":       bson.M{"$lte": now},
			"delivery_attempts": bson.M{"$gte": w.config.MaxAttempts},
		},
		bson.M{
			"$set": bson.M{
				"delivery_status":     "failed",
				"failed_at":           now,
				"last_delivery_error": "Delivery lease expired after maximum attempts",
			},
			"$unset": bson.M{
				"lease_until":     "",
				"lease_token":     "",
				"next_attempt_at": "",
			},
		},
	)
	if err != nil {
		return fmt.Errorf("recover exhausted delivery leases: %w", err)
	}

	if result.ModifiedCount > 0 {
		w.logger.Warn(
			"expired delivery leases exhausted",
			"count", result.ModifiedCount,
		)
	}

	return nil
}

func (w *Worker) processOne(ctx context.Context) (resultErr error) {
	if err := w.failExhaustedLeases(ctx); err != nil {
		return err
	}

	event, err := w.claim(ctx)
	if err != nil {
		return err
	}

	startedAt := time.Now().UTC()
	httpStatus := 0
	outcome := "failed"
	var attemptErr error

	// Persist attempt history independently of the delivery state.
	// Always emit the completion log, even when history is unavailable.
	defer func() {
		finishedAt := time.Now().UTC()
		durationMS := finishedAt.Sub(startedAt).Milliseconds()

		attempt := Attempt{
			EventID:       event.ID,
			AttemptNumber: event.Attempts,
			StartedAt:     startedAt,
			FinishedAt:    finishedAt,
			DurationMS:    durationMS,
			HTTPStatus:    httpStatus,
			Outcome:       outcome,
		}

		if attemptErr != nil {
			attempt.Error = attemptErr.Error()
		}

		if w.attempts == nil {
			w.logger.Warn(
				"delivery attempt history unavailable",
				"event_id", event.ID.Hex(),
				"attempt", event.Attempts,
			)
		} else {
			saveCtx, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)

			_, saveErr := w.attempts.InsertOne(saveCtx, attempt)
			cancel()

			if saveErr != nil {
				w.logger.Error(
					"delivery attempt history write failed",
					"event_id", event.ID.Hex(),
					"attempt", event.Attempts,
					"error_type", fmt.Sprintf("%T", saveErr),
				)
			}
		}

		// Keep the persisted history outcome compatible with existing tests.
		// Use a separate value for structured operational logging.
		logOutcome := outcome
		if outcome == "failed" &&
			event.Attempts < w.config.MaxAttempts {
			logOutcome = "retry_scheduled"
		}

		attrs := []any{
			"event_id", event.ID.Hex(),
			"attempt", event.Attempts,
			"outcome", logOutcome,
			"duration_ms", durationMS,
			"http_status", httpStatus,
		}

		if attemptErr != nil {
			attrs = append(
				attrs,
				"error_type",
				fmt.Sprintf("%T", attemptErr),
			)
		}

		switch logOutcome {
		case "delivered":
			w.logger.Info("webhook delivery attempt completed", attrs...)
		case "retry_scheduled":
			w.logger.Warn("webhook delivery attempt completed", attrs...)
		default:
			w.logger.Error("webhook delivery attempt completed", attrs...)
		}
	}()

	payload := webhookPayload{
		EventID:        event.ID.Hex(),
		Source:         event.Source,
		EventType:      event.EventType,
		Data:           event.Data,
		IdempotencyKey: event.IdempotencyKey,
		ReceivedAt:     event.ReceivedAt,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		attemptErr = err
		return w.recordFailure(ctx, event, err)
	}

	requestCtx, cancel := context.WithTimeout(
		ctx,
		w.config.RequestTimeout,
	)
	defer cancel()

	req, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		w.config.WebhookURL,
		bytes.NewReader(body),
	)
	if err != nil {
		attemptErr = err
		return w.recordFailure(ctx, event, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Relay-Webhook/1.0")
	req.Header.Set("Idempotency-Key", event.IdempotencyKey)
	req.Header.Set("X-Relay-Event-ID", event.ID.Hex())

	resp, err := w.client.Do(req)
	if err != nil {
		attemptErr = err
		return w.recordFailure(ctx, event, err)
	}
	defer resp.Body.Close()

	httpStatus = resp.StatusCode

	// Drain only a limited amount of the response body.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		attemptErr = fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
		return w.recordFailure(ctx, event, attemptErr)
	}

	// The receiver acknowledged delivery. Preserve this outcome even if
	// the subsequent event-document update fails.
	outcome = "delivered"

	result, err := w.events.UpdateOne(
		ctx,
		bson.M{
			"_id":             event.ID,
			"delivery_status": "processing",
			"lease_token":     event.LeaseToken,
		},
		bson.M{
			"$set": bson.M{
				"delivery_status": "delivered",
				"delivered_at":    time.Now().UTC(),
			},
			"$unset": bson.M{
				"lease_until":         "",
				"lease_token":         "",
				"next_attempt_at":     "",
				"last_delivery_error": "",
			},
		},
	)
	if err != nil {
		return fmt.Errorf("mark event delivered: %w", err)
	}
	if result.MatchedCount == 0 {
		return errors.New(
			"mark event delivered: event is no longer owned by this lease",
		)
	}

	return nil
}

// recordFailure preserves the existing attempt-history outcome of "failed".
// The completion logger derives "retry_scheduled" separately when applicable.
// A stale worker cannot change state after its lease token has been replaced.
func (w *Worker) recordFailure(
	ctx context.Context,
	event eventRecord,
	cause error,
) error {
	now := time.Now().UTC()

	set := bson.M{
		"last_delivery_error": cause.Error(),
	}

	unset := bson.M{
		"lease_until": "",
		"lease_token": "",
	}

	if event.Attempts >= w.config.MaxAttempts {
		set["delivery_status"] = "failed"
		set["failed_at"] = now
		unset["next_attempt_at"] = ""
	} else {
		set["delivery_status"] = "pending"
		set["next_attempt_at"] = now.Add(
			w.retryDelay(event.Attempts),
		)
	}

	result, err := w.events.UpdateOne(
		ctx,
		bson.M{
			"_id":             event.ID,
			"delivery_status": "processing",
			"lease_token":     event.LeaseToken,
		},
		bson.M{
			"$set":   set,
			"$unset": unset,
		},
	)
	if err != nil {
		// Avoid returning the original cause, which could contain a URL.
		return fmt.Errorf("record delivery failure: %w", err)
	}
	if result.MatchedCount == 0 {
		return errors.New(
			"record delivery failure: event is no longer owned by this lease",
		)
	}

	return nil
}

// retryDelay uses exponential backoff capped at five minutes,
// with up to 25% additional jitter.
func (w *Worker) retryDelay(attempt int) time.Duration {
	delay := 2 * time.Second

	for i := 1; i < attempt && delay < 5*time.Minute; i++ {
		delay *= 2
	}

	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}

	jitter := time.Duration(
		rand.Int63n(int64(delay/4) + 1),
	)

	return delay + jitter
}
