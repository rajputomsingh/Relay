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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"github.com/rajputomsingh/relay/internal/security"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config controls the multi-endpoint delivery worker.
//
// Per-endpoint settings (URL, signing secret, retry limit, event filter)
// live on the endpoint document and are read at claim time.
type Config struct {
	PollInterval   time.Duration
	RequestTimeout time.Duration
	LeaseDuration  time.Duration
	Concurrency    int
	Logger         *slog.Logger
}

// Worker processes deliveries for every enabled endpoint.
//
// Each Run call spawns Config.Concurrency independent goroutines. They
// share no state beyond the MongoDB collections; coordination happens
// entirely through the atomic claim operation on the deliveries
// collection.
type Worker struct {
	events     *mongo.Collection
	endpoints  *mongo.Collection
	deliveries *mongo.Collection
	attempts   *mongo.Collection
	config     Config
	logger     *slog.Logger
	client     *http.Client

	// Health signals. Written by the worker loop, read by the metrics
	// handler.
	healthMu              sync.RWMutex
	lastProcessedAt       time.Time
	processingErrorsTotal int64
	inFlight              atomic.Int64
}

// claimResult carries the delivery plus its loaded event and endpoint.
type claimResult struct {
	Delivery Delivery
	Event    eventRecord
	Endpoint endpoints.Endpoint
}

// eventRecord is the projection of the events document used for delivery.
type eventRecord struct {
	ID             bson.ObjectID  `bson:"_id"`
	Source         string         `bson:"source"`
	EventType      string         `bson:"event_type"`
	Data           map[string]any `bson:"data"`
	IdempotencyKey string         `bson:"idempotency_key"`
	ReceivedAt     time.Time      `bson:"received_at"`
}

// webhookPayload is the JSON body sent to every receiver.
type webhookPayload struct {
	EventID        string         `json:"event_id"`
	Source         string         `json:"source"`
	EventType      string         `json:"event_type"`
	Data           map[string]any `json:"data"`
	IdempotencyKey string         `json:"idempotency_key"`
	ReceivedAt     time.Time      `json:"received_at"`
}

// NewWorker constructs the multi-endpoint delivery worker.
func NewWorker(
	events *mongo.Collection,
	endpointsCollection *mongo.Collection,
	deliveries *mongo.Collection,
	attempts *mongo.Collection,
	cfg Config,
) *Worker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Worker{
		events:     events,
		endpoints:  endpointsCollection,
		deliveries: deliveries,
		attempts:   attempts,
		config:     cfg,
		logger:     logger,
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

// validateConfig validates worker settings.
func (w *Worker) validateConfig() error {
	switch {
	case w.config.PollInterval <= 0:
		return errors.New("poll interval must be positive")
	case w.config.RequestTimeout <= 0:
		return errors.New("request timeout must be positive")
	case w.config.LeaseDuration <= w.config.RequestTimeout:
		return errors.New(
			"lease duration must be greater than request timeout",
		)
	case w.config.Concurrency < 1 || w.config.Concurrency > 32:
		return errors.New("concurrency must be between 1 and 32")
	}

	return nil
}

// Run starts the bounded worker pool and blocks until ctx is cancelled.
//
// Each goroutine runs an independent workerLoop that polls for due
// deliveries. Because claim is atomic, the goroutines naturally
// distribute work without coordinating.
func (w *Worker) Run(ctx context.Context) {
	if err := w.validateConfig(); err != nil {
		w.logger.Error(
			"delivery worker configuration invalid",
			"reason", err.Error(),
		)
		return
	}

	w.logger.Info(
		"delivery worker started",
		"concurrency", w.config.Concurrency,
	)

	var wg sync.WaitGroup
	for i := 0; i < w.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.workerLoop(ctx)
		}()
	}

	wg.Wait()

	w.logger.Info("delivery worker stopped")
}

// workerLoop is one bounded worker. It polls for due deliveries until
// ctx is cancelled.
func (w *Worker) workerLoop(ctx context.Context) {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		err := w.processOne(ctx)

		w.healthMu.Lock()
		if err == nil {
			w.lastProcessedAt = time.Now().UTC()
		} else if !errors.Is(err, mongo.ErrNoDocuments) &&
			ctx.Err() == nil {
			w.processingErrorsTotal++
		}
		w.healthMu.Unlock()

		if err != nil &&
			!errors.Is(err, mongo.ErrNoDocuments) &&
			ctx.Err() == nil {
			w.logger.Error(
				"delivery worker processing failed",
				"error_type", fmt.Sprintf("%T", err),
			)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// LastProcessedAt returns the timestamp of the most recent successful
// processing cycle. Zero if no cycle has completed.
func (w *Worker) LastProcessedAt() time.Time {
	w.healthMu.RLock()
	defer w.healthMu.RUnlock()
	return w.lastProcessedAt
}

// ProcessingErrorsTotal returns the count of non-ErrNoDocuments errors
// encountered during processing since the worker started.
func (w *Worker) ProcessingErrorsTotal() int64 {
	w.healthMu.RLock()
	defer w.healthMu.RUnlock()
	return w.processingErrorsTotal
}

// InFlightDeliveries returns the number of deliveries currently being
// processed. Bounded above by Config.Concurrency.
func (w *Worker) InFlightDeliveries() int {
	return int(w.inFlight.Load())
}

// claim atomically assigns one due delivery to this worker.
// It then loads the associated event and endpoint.
func (w *Worker) claim(ctx context.Context) (claimResult, error) {
	if err := w.failExhaustedLeases(ctx); err != nil {
		return claimResult{}, err
	}

	now := time.Now().UTC()
	leaseToken := bson.NewObjectID().Hex()

	filter := bson.M{
		"$or": []bson.M{
			{
				"status": StatusPending,
				"$or": []bson.M{
					{"next_attempt_at": bson.M{"$exists": false}},
					{"next_attempt_at": bson.M{"$lte": now}},
				},
			},
			{
				"status":      StatusProcessing,
				"lease_until": bson.M{"$lte": now},
			},
		},
	}

	update := bson.M{
		"$set": bson.M{
			"status":      StatusProcessing,
			"lease_until": now.Add(w.config.LeaseDuration),
			"lease_token": leaseToken,
			"updated_at":  now,
		},
	}

	var delivery Delivery
	err := w.deliveries.FindOneAndUpdate(
		ctx,
		filter,
		update,
		options.FindOneAndUpdate().
			SetSort(bson.D{
				{Key: "next_attempt_at", Value: 1},
				{Key: "created_at", Value: 1},
			}).
			SetReturnDocument(options.After),
	).Decode(&delivery)
	if err != nil {
		return claimResult{}, err
	}

	var event eventRecord
	err = w.events.FindOne(ctx, bson.M{"_id": delivery.EventID}).Decode(&event)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if terr := w.recordTerminalFailure(
			ctx, delivery, "event not found",
		); terr != nil {
			w.logger.Error(
				"failed to mark delivery as failed for missing event",
				"delivery_id", delivery.ID.Hex(),
				"error_type", fmt.Sprintf("%T", terr),
			)
		}
		return claimResult{}, mongo.ErrNoDocuments
	}
	if err != nil {
		return claimResult{}, fmt.Errorf("load event: %w", err)
	}

	var ep endpoints.Endpoint
	err = w.endpoints.FindOne(
		ctx, bson.M{"_id": delivery.EndpointID},
	).Decode(&ep)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if terr := w.recordTerminalFailure(
			ctx, delivery, "endpoint not found",
		); terr != nil {
			w.logger.Error(
				"failed to mark delivery as failed for missing endpoint",
				"delivery_id", delivery.ID.Hex(),
				"error_type", fmt.Sprintf("%T", terr),
			)
		}
		return claimResult{}, mongo.ErrNoDocuments
	}
	if err != nil {
		return claimResult{}, fmt.Errorf("load endpoint: %w", err)
	}

	if !ep.Enabled {
		if terr := w.recordTerminalFailure(
			ctx, delivery, "endpoint disabled",
		); terr != nil {
			w.logger.Error(
				"failed to mark delivery as failed for disabled endpoint",
				"delivery_id", delivery.ID.Hex(),
				"error_type", fmt.Sprintf("%T", terr),
			)
		}
		return claimResult{}, mongo.ErrNoDocuments
	}

	return claimResult{
		Delivery: delivery,
		Event:    event,
		Endpoint: ep,
	}, nil
}

// failExhaustedLeases permanently fails expired processing deliveries
// whose attempts have already reached the endpoint's retry limit.
func (w *Worker) failExhaustedLeases(ctx context.Context) error {
	now := time.Now().UTC()

	result, err := w.deliveries.UpdateMany(
		ctx,
		bson.M{
			"status":      StatusProcessing,
			"lease_until": bson.M{"$lte": now},
			"$expr": bson.M{
				"$gte": bson.A{"$attempts", "$max_attempts"},
			},
		},
		bson.M{
			"$set": bson.M{
				"status":     StatusFailed,
				"failed_at":  now,
				"updated_at": now,
				"last_error": "Delivery lease expired after maximum attempts",
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

// processOne claims and executes one delivery.
func (w *Worker) processOne(ctx context.Context) error {
	claim, err := w.claim(ctx)
	if err != nil {
		return err
	}

	delivery := claim.Delivery
	event := claim.Event
	endpoint := claim.Endpoint

	w.inFlight.Add(1)
	defer w.inFlight.Add(-1)

	startedAt := time.Now().UTC()
	httpStatus := 0
	var attemptErr error
	outcome := "failed"

	defer func() {
		finishedAt := time.Now().UTC()
		durationMS := finishedAt.Sub(startedAt).Milliseconds()

		attempt := Attempt{
			EventID:       event.ID,
			EndpointID:    endpoint.ID,
			AttemptNumber: delivery.Attempts + 1,
			StartedAt:     startedAt,
			FinishedAt:    finishedAt,
			DurationMS:    durationMS,
			HTTPStatus:    httpStatus,
			Outcome:       outcome,
		}
		if attemptErr != nil {
			attempt.Error = attemptErr.Error()
		}

		saveCtx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		_, saveErr := w.attempts.InsertOne(saveCtx, attempt)
		cancel()

		if saveErr != nil {
			w.logger.Error(
				"delivery attempt history write failed",
				"delivery_id", delivery.ID.Hex(),
				"event_id", event.ID.Hex(),
				"endpoint_id", endpoint.ID.Hex(),
				"error_type", fmt.Sprintf("%T", saveErr),
			)
		}

		attrs := []any{
			"delivery_id", delivery.ID.Hex(),
			"event_id", event.ID.Hex(),
			"endpoint_id", endpoint.ID.Hex(),
			"attempt", attempt.AttemptNumber,
			"outcome", outcome,
			"duration_ms", durationMS,
			"http_status", httpStatus,
		}
		if attemptErr != nil {
			attrs = append(
				attrs, "error_type", fmt.Sprintf("%T", attemptErr),
			)
		}

		switch outcome {
		case "delivered":
			w.logger.Info("webhook delivery attempt completed", attrs...)
		default:
			w.logger.Warn("webhook delivery attempt completed", attrs...)
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
		return w.recordFailure(ctx, delivery, err.Error())
	}

	requestCtx, cancel := context.WithTimeout(
		ctx, w.config.RequestTimeout,
	)
	defer cancel()

	req, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		endpoint.URL,
		bytes.NewReader(body),
	)
	if err != nil {
		attemptErr = err
		return w.recordFailure(ctx, delivery, err.Error())
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Relay-Webhook/1.0")
	req.Header.Set("Idempotency-Key", event.IdempotencyKey)
	req.Header.Set("X-Relay-Event-ID", event.ID.Hex())

	if endpoint.SigningSecret != "" {
		signer, err := security.NewSigner(endpoint.SigningSecret)
		if err != nil {
			attemptErr = err
			return w.recordFailure(ctx, delivery, err.Error())
		}
		ts := security.TimestampNow()
		signature := signer.SignBody(ts, body)
		req.Header.Set(
			security.TimestampHeader, strconv.FormatInt(ts, 10),
		)
		req.Header.Set(security.SignatureHeader, signature)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		attemptErr = err
		return w.recordFailure(ctx, delivery, err.Error())
	}
	defer resp.Body.Close()

	httpStatus = resp.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		attemptErr = fmt.Errorf(
			"webhook returned HTTP %d", resp.StatusCode,
		)
		return w.recordFailure(ctx, delivery, attemptErr.Error())
	}

	outcome = "delivered"
	return w.recordSuccess(ctx, delivery)
}

// recordSuccess marks the delivery delivered, fenced by lease token.
func (w *Worker) recordSuccess(
	ctx context.Context,
	delivery Delivery,
) error {
	now := time.Now().UTC()

	result, err := w.deliveries.UpdateOne(
		ctx,
		bson.M{
			"_id":         delivery.ID,
			"status":      StatusProcessing,
			"lease_token": delivery.LeaseToken,
		},
		bson.M{
			"$set": bson.M{
				"status":       StatusDelivered,
				"delivered_at": now,
				"updated_at":   now,
			},
			"$unset": bson.M{
				"lease_until":     "",
				"lease_token":     "",
				"next_attempt_at": "",
				"last_error":      "",
			},
		},
	)
	if err != nil {
		return fmt.Errorf("mark delivery delivered: %w", err)
	}
	if result.MatchedCount == 0 {
		return errors.New(
			"mark delivery delivered: no longer owned by this lease",
		)
	}

	return nil
}

// recordFailure schedules a retry or marks the delivery failed,
// fenced by lease token. Used for transient failures (network,
// non-2xx HTTP response, marshalling errors).
func (w *Worker) recordFailure(
	ctx context.Context,
	delivery Delivery,
	message string,
) error {
	now := time.Now().UTC()

	nextAttempt := delivery.Attempts + 1

	set := bson.M{
		"last_error": message,
		"attempts":   nextAttempt,
		"updated_at": now,
	}
	unset := bson.M{
		"lease_until": "",
		"lease_token": "",
	}

	if nextAttempt >= delivery.MaxAttempts {
		set["status"] = StatusFailed
		set["failed_at"] = now
		unset["next_attempt_at"] = ""
	} else {
		set["status"] = StatusPending
		set["next_attempt_at"] = now.Add(
			w.retryDelay(nextAttempt),
		)
	}

	result, err := w.deliveries.UpdateOne(
		ctx,
		bson.M{
			"_id":         delivery.ID,
			"status":      StatusProcessing,
			"lease_token": delivery.LeaseToken,
		},
		bson.M{
			"$set":   set,
			"$unset": unset,
		},
	)
	if err != nil {
		return fmt.Errorf("record delivery failure: %w", err)
	}
	if result.MatchedCount == 0 {
		return errors.New(
			"record delivery failure: no longer owned by this lease",
		)
	}

	return nil
}

// recordTerminalFailure marks a delivery failed without scheduling
// a retry. Used when the delivery cannot succeed regardless of retries:
// missing event, missing endpoint, disabled endpoint.
func (w *Worker) recordTerminalFailure(
	ctx context.Context,
	delivery Delivery,
	message string,
) error {
	now := time.Now().UTC()

	result, err := w.deliveries.UpdateOne(
		ctx,
		bson.M{
			"_id":         delivery.ID,
			"status":      StatusProcessing,
			"lease_token": delivery.LeaseToken,
		},
		bson.M{
			"$set": bson.M{
				"status":     StatusFailed,
				"failed_at":  now,
				"updated_at": now,
				"last_error": message,
			},
			"$unset": bson.M{
				"lease_until":     "",
				"lease_token":     "",
				"next_attempt_at": "",
			},
		},
	)
	if err != nil {
		return fmt.Errorf("record terminal delivery failure: %w", err)
	}
	if result.MatchedCount == 0 {
		return errors.New(
			"record terminal delivery failure: no longer owned by this lease",
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
