package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collectResult reads the benchmark database and the mock server state
// and assembles a full runResult.
func collectResult(
	ctx context.Context,
	opts runnerOptions,
	startedAt, finishedAt time.Time,
	drainSeconds float64,
) (runResult, error) {
	deliveriesCol := opts.database.Collection("deliveries")
	attemptsCol := opts.database.Collection("delivery_attempts")

	final, err := finalStatusCounts(ctx, deliveriesCol)
	if err != nil {
		return runResult{}, err
	}

	attemptDurations, err := readAttemptDurations(ctx, attemptsCol)
	if err != nil {
		return runResult{}, err
	}

	queueToFirst, queueToDelivered, err := readQueueLatencies(
		ctx, deliveriesCol, attemptsCol,
	)
	if err != nil {
		return runResult{}, err
	}

	hostname, _ := os.Hostname()

	successful := final.Delivered
	completed := final.Delivered + final.Failed

	failureRate := 0.0
	if completed > 0 {
		failureRate = float64(final.Failed) / float64(completed)
	}

	serverSnap := opts.server.snapshot()
	httpDurations := opts.server.durationSnapshot()

	result := runResult{
		Config: runConfig{
			Concurrency:       opts.cfg.Concurrency,
			Behavior:          string(opts.cfg.Behavior),
			Count:             opts.cfg.Count,
			Warmup:            opts.cfg.WarmupDeliveries,
			MaxAttempts:       opts.cfg.MaxAttemptsOrDefault(),
			PollInterval:      benchmarkPollInterval,
			RequestTimeout:    productionRequestTimeout,
			LeaseDuration:     productionLeaseDuration,
			SlowDelay:         opts.cfg.SlowDelay,
			TransientFailures: opts.cfg.TransientFailures,
			Database:          opts.dbName,
		},
		Environment: environmentInfo{
			GoVersion:  runtime.Version(),
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			Hostname:   hostname,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		},
		Timing: timingResult{
			DrainSeconds: drainSeconds,
		},
		Throughput: throughputResult{
			SuccessfulPerSecond: float64(successful) / drainSeconds,
			AttemptedPerSecond: float64(
				serverSnap.Requests,
			) / drainSeconds,
			SuccessfulTotal:  successful,
			FailedTotal:      final.Failed,
			PendingRemaining: final.Pending,
			FailureRate:      failureRate,
		},
		Attempts: attemptStats{
			Count:         len(attemptDurations),
			DurationP50MS: percentileMS(attemptDurations, 0.50),
			DurationP95MS: percentileMS(attemptDurations, 0.95),
			DurationP99MS: percentileMS(attemptDurations, 0.99),
		},
		HTTP: httpStats{
			Requests:  serverSnap.Requests,
			Successes: serverSnap.Successes,
			Failures:  serverSnap.Failures,
			P50MS:     percentileMS(httpDurations, 0.50),
			P95MS:     percentileMS(httpDurations, 0.95),
			P99MS:     percentileMS(httpDurations, 0.99),
		},
		Queue: queueStats{
			ToFirstAttemptP50MS: percentileMS(queueToFirst, 0.50),
			ToFirstAttemptP95MS: percentileMS(queueToFirst, 0.95),
			ToFirstAttemptP99MS: percentileMS(queueToFirst, 0.99),
			ToDeliveredP50MS:    percentileMS(queueToDelivered, 0.50),
			ToDeliveredP95MS:    percentileMS(queueToDelivered, 0.95),
			ToDeliveredP99MS:    percentileMS(queueToDelivered, 0.99),
		},
		Final: final,
		Server: serverStats{
			Requests:      serverSnap.Requests,
			Successes:     serverSnap.Successes,
			Failures:      serverSnap.Failures,
			TransientHits: serverSnap.TransientHits,
		},
		Mongo: mongoStats{
			Available: false,
			Reason:    "not collected",
		},
	}

	return result, nil
}

// finalStatusCounts returns counts of each delivery status.
func finalStatusCounts(
	ctx context.Context,
	deliveriesCol *mongo.Collection,
) (finalState, error) {
	pending, processing, delivered, failed, err :=
		countByStatus(ctx, deliveriesCol)
	if err != nil {
		return finalState{}, err
	}
	return finalState{
		Pending:    pending,
		Processing: processing,
		Delivered:  delivered,
		Failed:     failed,
	}, nil
}

// readAttemptDurations returns every attempt's duration in milliseconds.
func readAttemptDurations(
	ctx context.Context,
	attemptsCol *mongo.Collection,
) ([]int64, error) {
	cursor, err := attemptsCol.Find(
		ctx,
		bson.M{},
		options.Find().SetProjection(bson.M{"duration_ms": 1}),
	)
	if err != nil {
		return nil, fmt.Errorf("find attempts: %w", err)
	}
	defer cursor.Close(ctx)

	out := make([]int64, 0)
	for cursor.Next(ctx) {
		var row struct {
			DurationMS int64 `bson:"duration_ms"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode attempt: %w", err)
		}
		out = append(out, row.DurationMS)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempts: %w", err)
	}
	return out, nil
}

// readQueueLatencies returns two slices, both in milliseconds:
//
//   - toFirstAttempt: earliest attempt's started_at minus delivery's
//     created_at.
//   - toDelivered: delivery's delivered_at minus delivery's
//     created_at.
//
// Deliveries without delivered_at are excluded from toDelivered.
// Deliveries without a recorded attempt are excluded from
// toFirstAttempt.
func readQueueLatencies(
	ctx context.Context,
	deliveriesCol *mongo.Collection,
	attemptsCol *mongo.Collection,
) (toFirstAttempt, toDelivered []int64, err error) {
	cursor, err := deliveriesCol.Find(
		ctx,
		bson.M{"status": "delivered"},
		options.Find().SetProjection(bson.M{
			"_id":          1,
			"event_id":     1,
			"created_at":   1,
			"delivered_at": 1,
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("find delivered: %w", err)
	}
	defer cursor.Close(ctx)

	type deliveredRow struct {
		ID          bson.ObjectID `bson:"_id"`
		EventID     bson.ObjectID `bson:"event_id"`
		CreatedAt   time.Time     `bson:"created_at"`
		DeliveredAt time.Time     `bson:"delivered_at"`
	}

	toFirstAttempt = make([]int64, 0)
	toDelivered = make([]int64, 0)

	for cursor.Next(ctx) {
		var row deliveredRow
		if err := cursor.Decode(&row); err != nil {
			return nil, nil, fmt.Errorf("decode delivery: %w", err)
		}

		if !row.DeliveredAt.IsZero() {
			toDelivered = append(
				toDelivered,
				row.DeliveredAt.Sub(row.CreatedAt).Milliseconds(),
			)
		}

		// Attempts are keyed by event_id, not delivery_id. Fetch the
		// earliest attempt for this delivery's event.
		var attemptRow struct {
			StartedAt time.Time `bson:"started_at"`
		}
		err := attemptsCol.FindOne(
			ctx,
			bson.M{"event_id": row.EventID},
			options.FindOne().
				SetSort(bson.D{{Key: "started_at", Value: 1}}),
		).Decode(&attemptRow)

		if err == nil && !attemptRow.StartedAt.IsZero() {
			toFirstAttempt = append(
				toFirstAttempt,
				attemptRow.StartedAt.Sub(row.CreatedAt).Milliseconds(),
			)
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate delivered: %w", err)
	}

	return toFirstAttempt, toDelivered, nil
}

// percentileMS returns the p-th percentile of samples in milliseconds.
// samples need not be sorted.
func percentileMS(samples []int64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]int64, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx])
}

// MaxAttemptsOrDefault returns the configured retry limit, or a
// conservative default when unset.
func (c Config) MaxAttemptsOrDefault() int {
	if c.MaxAttempts > 0 {
		return c.MaxAttempts
	}
	return 8
}
