package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// runResult captures everything measured during one benchmark run.
// It is serialized to JSON by report.go.
type runResult struct {
	Config      runConfig        `json:"config"`
	Environment environmentInfo  `json:"environment"`
	Timing      timingResult     `json:"timing"`
	Throughput  throughputResult `json:"throughput"`
	Attempts    attemptStats     `json:"attempts"`
	HTTP        httpStats        `json:"http"`
	Queue       queueStats       `json:"queue"`
	Final       finalState       `json:"final"`
	Server      serverStats      `json:"server"`
	Mongo       mongoStats       `json:"mongo"`
	Errors      []string         `json:"errors,omitempty"`
}

type runConfig struct {
	Concurrency       int           `json:"concurrency"`
	Behavior          string        `json:"behavior"`
	Count             int           `json:"count"`
	Warmup            int           `json:"warmup"`
	MaxAttempts       int           `json:"max_attempts"`
	PollInterval      time.Duration `json:"poll_interval_ns"`
	RequestTimeout    time.Duration `json:"request_timeout_ns"`
	LeaseDuration     time.Duration `json:"lease_duration_ns"`
	SlowDelay         time.Duration `json:"slow_delay_ns,omitempty"`
	TransientFailures int           `json:"transient_failures,omitempty"`
	OutageDuration    time.Duration `json:"outage_duration_ns,omitempty"`
	Database          string        `json:"database"`
}

type environmentInfo struct {
	GoVersion  string    `json:"go_version"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	Hostname   string    `json:"hostname,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type timingResult struct {
	DrainSeconds         float64 `json:"drain_seconds"`
	RecoveryDrainSeconds float64 `json:"recovery_drain_seconds,omitempty"`
}

type throughputResult struct {
	SuccessfulPerSecond float64 `json:"successful_per_second"`
	AttemptedPerSecond  float64 `json:"attempted_per_second"`
	SuccessfulTotal     int64   `json:"successful_total"`
	FailedTotal         int64   `json:"failed_total"`
	PendingRemaining    int64   `json:"pending_remaining"`
	FailureRate         float64 `json:"failure_rate"`
}

type attemptStats struct {
	Count         int           `json:"count"`
	DurationP50MS float64       `json:"duration_p50_ms"`
	DurationP95MS float64       `json:"duration_p95_ms"`
	DurationP99MS float64       `json:"duration_p99_ms"`
	Distribution  map[int]int64 `json:"distribution,omitempty"`
}

type httpStats struct {
	Requests  int64   `json:"requests"`
	Successes int64   `json:"successes"`
	Failures  int64   `json:"failures"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	P99MS     float64 `json:"p99_ms"`
}

type queueStats struct {
	ToFirstAttemptP50MS float64 `json:"to_first_attempt_p50_ms"`
	ToFirstAttemptP95MS float64 `json:"to_first_attempt_p95_ms"`
	ToFirstAttemptP99MS float64 `json:"to_first_attempt_p99_ms"`
	ToDeliveredP50MS    float64 `json:"to_delivered_p50_ms"`
	ToDeliveredP95MS    float64 `json:"to_delivered_p95_ms"`
	ToDeliveredP99MS    float64 `json:"to_delivered_p99_ms"`
	RetryGapP50MS       float64 `json:"retry_gap_p50_ms,omitempty"`
	RetryGapP95MS       float64 `json:"retry_gap_p95_ms,omitempty"`
}

type finalState struct {
	Pending    int64 `json:"pending"`
	Processing int64 `json:"processing"`
	Delivered  int64 `json:"delivered"`
	Failed     int64 `json:"failed"`
}

type serverStats struct {
	Requests      int64 `json:"requests"`
	Successes     int64 `json:"successes"`
	Failures      int64 `json:"failures"`
	TransientHits int64 `json:"transient_hits,omitempty"`
}

type mongoStats struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Version   string `json:"version,omitempty"`
}

// productionTimings mirrors the values in cmd/relay/main.go so
// benchmarks reflect the same worker configuration used in production.
const (
	productionPollInterval   = 2 * time.Second
	productionRequestTimeout = 10 * time.Second
	productionLeaseDuration  = 30 * time.Second
)

// benchmarkPollInterval overrides production polling with a faster
// value so short benchmark runs actually drain. The worker's own
// throughput is bounded by this polling rate, not by the worker pool.
const benchmarkPollInterval = 200 * time.Millisecond

// runnerOptions bundles the inputs to run().
type runnerOptions struct {
	cfg      Config
	database *mongo.Database
	dbName   string
	server   *mockServer
	seedData seedData
	logger   *slog.Logger
}

// run drives one full benchmark: start the worker, wait for the
// backlog to drain, capture results, and shut down.
func run(
	ctx context.Context,
	opts runnerOptions,
) (runResult, error) {
	if opts.seedData.EndpointID.IsZero() {
		return runResult{}, errNoSeedData
	}

	eventsCol := opts.database.Collection("events")
	endpointsCol := opts.database.Collection("endpoints")
	deliveriesCol := opts.database.Collection("deliveries")
	attemptsCol := opts.database.Collection("delivery_attempts")

	worker := delivery.NewWorker(
		eventsCol,
		endpointsCol,
		deliveriesCol,
		attemptsCol,
		delivery.Config{
			PollInterval:   benchmarkPollInterval,
			RequestTimeout: productionRequestTimeout,
			LeaseDuration:  productionLeaseDuration,
			Concurrency:    opts.cfg.Concurrency,
			Logger:         opts.logger,
		},
	)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(runCtx)
	}()

	startedAt := time.Now().UTC()

	// Poll progress until all deliveries reach a terminal state or
	// the drain timeout fires.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	drainDeadline := time.Now().Add(opts.cfg.DrainTimeout)

	for {
		pending, processing, delivered, failed, err :=
			countByStatus(ctx, deliveriesCol)
		if err != nil {
			cancelRun()
			<-workerDone
			return runResult{}, fmt.Errorf("count by status: %w", err)
		}

		if !opts.cfg.Quiet {
			opts.logger.Info(
				"progress",
				"pending", pending,
				"processing", processing,
				"delivered", delivered,
				"failed", failed,
				"elapsed", time.Since(startedAt).Truncate(time.Second).String(),
			)
		}

		if pending == 0 && processing == 0 {
			break
		}
		if time.Now().After(drainDeadline) {
			cancelRun()
			<-workerDone
			return runResult{}, fmt.Errorf(
				"drain timeout exceeded: pending=%d processing=%d",
				pending, processing,
			)
		}

		select {
		case <-ctx.Done():
			cancelRun()
			<-workerDone
			return runResult{}, ctx.Err()
		case <-ticker.C:
		}
	}

	finishedAt := time.Now().UTC()
	drainSeconds := finishedAt.Sub(startedAt).Seconds()

	cancelRun()
	<-workerDone

	result, err := collectResult(
		ctx,
		opts,
		startedAt,
		finishedAt,
		drainSeconds,
	)
	if err != nil {
		return runResult{}, err
	}

	return result, nil
}

// countByStatus returns the count of deliveries in each status.
func countByStatus(
	ctx context.Context,
	deliveriesCol *mongo.Collection,
) (pending, processing, delivered, failed int64, err error) {
	pending, err = deliveriesCol.CountDocuments(
		ctx, bson.M{"status": "pending"},
	)
	if err != nil {
		return
	}
	processing, err = deliveriesCol.CountDocuments(
		ctx, bson.M{"status": "processing"},
	)
	if err != nil {
		return
	}
	delivered, err = deliveriesCol.CountDocuments(
		ctx, bson.M{"status": "delivered"},
	)
	if err != nil {
		return
	}
	failed, err = deliveriesCol.CountDocuments(
		ctx, bson.M{"status": "failed"},
	)
	return
}
