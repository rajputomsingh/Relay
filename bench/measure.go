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
	pendingSamples []int64,
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

	attemptDistribution, err := readAttemptDistribution(ctx, attemptsCol)
	if err != nil {
		return runResult{}, err
	}

	retryGaps, err := readRetryGaps(ctx, attemptsCol)
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

	// RecoveryDrainSeconds measures the time from the end of a
	// configured outage to the moment all deliveries reach terminal
	// state. It is zero for behaviors with no outage window.
	recoverySeconds := 0.0
	if until := opts.server.OutageUntil(); !until.IsZero() {
		if finishedAt.After(until) {
			recoverySeconds = finishedAt.Sub(until).Seconds()
		}
	}

	serverSnap := opts.server.snapshot()
	httpDurations := opts.server.durationSnapshot()
	mongoMetrics := collectMongoStatus(ctx, opts.database)

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
			OutageDuration:    opts.cfg.OutageDuration,
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
			DrainSeconds:         drainSeconds,
			RecoveryDrainSeconds: recoverySeconds,
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
			Distribution:  attemptDistribution,
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
			RetryGapP50MS:       percentileMS(retryGaps, 0.50),
			RetryGapP95MS:       percentileMS(retryGaps, 0.95),
			QueueGrowthMax:      maxInt64(pendingSamples),
			QueueGrowthP50:      percentileMS(pendingSamples, 0.50),
			QueueGrowthP95:      percentileMS(pendingSamples, 0.95),
			QueueSamples:        len(pendingSamples),
		},
		Final: final,
		Server: serverStats{
			Requests:      serverSnap.Requests,
			Successes:     serverSnap.Successes,
			Failures:      serverSnap.Failures,
			TransientHits: serverSnap.TransientHits,
		},
		Mongo: mongoMetrics,
	}

	return result, nil
}

// numeric coerces any BSON numeric value to int64. The mongo-driver
// decodes serverStatus counters differently depending on the server
// version and BSON wire format; the concrete Go type can be int32,
// int64, or float64.
func numeric(v any) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// collectMongoStatus reads serverStatus counters that indicate whether
// MongoDB is the throughput bottleneck. Returns an unavailable
// mongoStats if the server does not grant permission or the command
// fails.
func collectMongoStatus(
	ctx context.Context,
	db *mongo.Database,
) mongoStats {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result bson.M
	err := db.Client().Database("admin").RunCommand(cmdCtx, bson.D{
		{Key: "serverStatus", Value: 1},
	}).Decode(&result)
	if err != nil {
		return mongoStats{
			Available: false,
			Reason:    err.Error(),
		}
	}

	stats := mongoStats{Available: true}

	if version, ok := result["version"].(string); ok {
		stats.Version = version
	}

	// opcounters and connections are embedded documents. The driver
	// decodes embedded documents as bson.D when the parent is bson.M.
	if ops, ok := result["opcounters"].(bson.D); ok {
		for _, elem := range ops {
			switch elem.Key {
			case "insert":
				stats.OpcountersInsert = numeric(elem.Value)
			case "query":
				stats.OpcountersQuery = numeric(elem.Value)
			case "update":
				stats.OpcountersUpdate = numeric(elem.Value)
			}
		}
	}

	if conns, ok := result["connections"].(bson.D); ok {
		for _, elem := range conns {
			if elem.Key == "current" {
				stats.ConnectionsCurrent = int(numeric(elem.Value))
			}
		}
	}

	return stats
}

// maxInt64 returns the largest value in xs, or zero for an empty slice.
func maxInt64(xs []int64) int64 {
	var m int64
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
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

// readAttemptDistribution returns a count of attempts grouped by
// attempt number.
//
// Example: {1: 200, 2: 200, 3: 50} means 200 deliveries made at least
// one attempt, 200 made at least two, and 50 made three.
func readAttemptDistribution(
	ctx context.Context,
	attemptsCol *mongo.Collection,
) (map[int]int64, error) {
	cursor, err := attemptsCol.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   "$attempt_number",
			"count": bson.M{"$sum": 1},
		}}},
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate attempts: %w", err)
	}
	defer cursor.Close(ctx)

	out := make(map[int]int64)
	for cursor.Next(ctx) {
		var row struct {
			Attempt int   `bson:"_id"`
			Count   int64 `bson:"count"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode distribution: %w", err)
		}
		out[row.Attempt] = row.Count
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate distribution: %w", err)
	}
	return out, nil
}

// readRetryGaps returns the gap in milliseconds between consecutive
// attempts of the same delivery.
func readRetryGaps(
	ctx context.Context,
	attemptsCol *mongo.Collection,
) ([]int64, error) {
	cursor, err := attemptsCol.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$sort", Value: bson.D{
			{Key: "event_id", Value: 1},
			{Key: "started_at", Value: 1},
		}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":     "$event_id",
			"started": bson.M{"$push": "$started_at"},
		}}},
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate retry gaps: %w", err)
	}
	defer cursor.Close(ctx)

	out := make([]int64, 0)
	for cursor.Next(ctx) {
		var row struct {
			Started []time.Time `bson:"started"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode retry gap: %w", err)
		}
		for i := 1; i < len(row.Started); i++ {
			gap := row.Started[i].Sub(row.Started[i-1]).Milliseconds()
			out = append(out, gap)
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate retry gaps: %w", err)
	}
	return out, nil
}

// readQueueLatencies returns two slices, both in milliseconds:
//
//   - toFirstAttempt: earliest attempt's started_at minus delivery's
//     created_at.
//   - toDelivered: delivery's delivered_at minus delivery's
//     created_at.
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
