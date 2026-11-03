package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// matrixResult pairs a run with its configuration for the summary report.
type matrixResult struct {
	Result runResult
	Config Config
}

// runMatrix executes every (concurrency, behavior) combination and
// writes a consolidated summary Markdown file.
func runMatrix(
	ctx context.Context,
	cfg Config,
	client *mongo.Client,
	logger *slog.Logger,
) error {
	behaviors := matrixBehaviors(cfg)

	results := make(
		[]matrixResult,
		0,
		len(cfg.ConcurrencyLevels)*len(behaviors),
	)

	for _, concurrency := range cfg.ConcurrencyLevels {
		for _, behavior := range behaviors {
			child := cfg
			child.Concurrency = concurrency
			child.Behavior = behavior
			child.Matrix = false
			child.Database = ""
			child.Quiet = true

			logger.Info(
				"matrix: starting combination",
				"concurrency", concurrency,
				"behavior", string(behavior),
			)

			result, err := runSingle(ctx, child, client, logger)
			if err != nil {
				return fmt.Errorf(
					"matrix combination c=%d b=%s: %w",
					concurrency,
					behavior,
					err,
				)
			}

			results = append(results, matrixResult{
				Result: result,
				Config: child,
			})

			summarize(result)
		}
	}

	if err := writeMatrixSummary(cfg, results); err != nil {
		return err
	}

	logger.Info(
		"matrix summary written",
		"path", filepath.Join(cfg.OutputDir, "summary.md"),
	)
	return nil
}

// matrixBehaviors returns the behaviors swept by --matrix.
//
// If the caller set --behavior explicitly (cfg.Behavior differs from
// its default BehaviorFast), the sweep narrows to that single
// behavior so targeted verification runs complete quickly.
//
// Otherwise the sweep covers one behavior from every outcome class
// the harness exercises: immediate success, latency-bound success,
// permanent 5xx, client-side timeout, transient 5xx that recovers,
// an outage window that recovers, and a permanently unavailable
// downstream. Five of the seven produce retries, so the
// attempt-breakdown table added in issue #50 renders on every full
// matrix run.
//
// RELAY_BENCH_BEHAVIORS=<csv> overrides both, letting CI or a
// developer pick an explicit subset without a code change. Unknown
// names are dropped; if the override yields no recognised
// behaviors, the full default list is used so a typo can't silently
// shrink the sweep to zero.
func matrixBehaviors(cfg Config) []Behavior {
	if raw := strings.TrimSpace(os.Getenv("RELAY_BENCH_BEHAVIORS")); raw != "" {
		if out := parseBehaviorList(raw); len(out) > 0 {
			return out
		}
	}

	if cfg.Behavior != BehaviorFast {
		return []Behavior{cfg.Behavior}
	}

	return []Behavior{
		BehaviorFast,
		BehaviorSlow,
		BehaviorHTTP500,
		BehaviorTimeout,
		BehaviorTransient,
		BehaviorOutage,
		BehaviorUnavailable,
	}
}

// parseBehaviorList splits a comma-separated override and keeps only
// names that match a known Behavior constant. Empty input and
// unrecognised names produce an empty slice so the caller can fall
// back to the default sweep.
func parseBehaviorList(raw string) []Behavior {
	known := map[Behavior]bool{
		BehaviorFast:        true,
		BehaviorSlow:        true,
		BehaviorHTTP500:     true,
		BehaviorTimeout:     true,
		BehaviorTransient:   true,
		BehaviorOutage:      true,
		BehaviorUnavailable: true,
	}

	parts := strings.Split(raw, ",")
	out := make([]Behavior, 0, len(parts))
	for _, p := range parts {
		b := Behavior(strings.TrimSpace(p))
		if known[b] {
			out = append(out, b)
		}
	}
	return out
}

// runSingle executes one benchmark combination with a fresh database.
func runSingle(
	ctx context.Context,
	cfg Config,
	client *mongo.Client,
	logger *slog.Logger,
) (runResult, error) {
	// The production delivery.Worker caps concurrency at 32. Reject
	// configurations that would pass harness validation but fail in
	// the worker after seeding thousands of deliveries, leaving the
	// harness hung waiting for a drain that can never complete.
	if cfg.Concurrency > 32 {
		return runResult{}, fmt.Errorf(
			"concurrency %d exceeds worker maximum of 32",
			cfg.Concurrency,
		)
	}

	dbName := cfg.Database
	if dbName == "" {
		dbName = uniqueDatabaseName()
	}
	db := client.Database(dbName)

	defer func() {
		dropCtx, dropCancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer dropCancel()
		if err := dropBenchmarkDatabase(dropCtx, db); err != nil {
			logger.Warn(
				"failed to drop benchmark database",
				"database", dbName,
				"error", err,
			)
		}
	}()

	if err := ensureIndexes(ctx, db); err != nil {
		return runResult{}, err
	}

	server := newMockServer(
		cfg.Behavior,
		cfg.SlowDelay,
		cfg.TransientFailures,
		cfg.OutageDuration,
	)
	defer server.Close()

	logServerStart(logger, server)

	seeded, err := seed(
		ctx,
		db,
		server.URL(),
		cfg.Count,
		cfg.MaxAttemptsOrDefault(),
	)
	if err != nil {
		return runResult{}, err
	}

	logger.Info(
		"deliveries seeded",
		"count", len(seeded.DeliveryIDs),
		"endpoint_id", seeded.EndpointID.Hex(),
	)

	result, err := run(ctx, runnerOptions{
		cfg:      cfg,
		database: db,
		dbName:   dbName,
		server:   server,
		seedData: seeded,
		logger:   logger,
	})
	if err != nil {
		return runResult{}, err
	}

	if _, err := writeJSONResult(cfg, result); err != nil {
		return runResult{}, err
	}

	return result, nil
}

// writeMatrixSummary writes a consolidated Markdown table.
func writeMatrixSummary(
	cfg Config,
	results []matrixResult,
) error {
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir output: %w", err)
	}

	var sb strings.Builder

	sb.WriteString("# Benchmark Summary\n\n")
	sb.WriteString(
		fmt.Sprintf(
			"Generated: %s\n\n",
			time.Now().UTC().Format(time.RFC3339),
		),
	)
	sb.WriteString(
		fmt.Sprintf("Deliveries per run: %d\n\n", cfg.Count),
	)

	sb.WriteString(
		"| Concurrency | Behavior | Drain (s) | Throughput (deliv/s) | ",
	)
	sb.WriteString(
		"Attempts/s | Attempted (req/s) | Delivered | Failed | Failure Rate | ",
	)
	sb.WriteString("HTTP p50 (ms) | HTTP p95 (ms) | HTTP p99 (ms) |\n")
	sb.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")

	for _, r := range results {
		attemptsPerSecond := 0.0
		if r.Result.Timing.DrainSeconds > 0 {
			attemptsPerSecond = float64(r.Result.Attempts.Count) /
				r.Result.Timing.DrainSeconds
		}

		sb.WriteString(fmt.Sprintf(
			"| %d | %s | %.2f | %.2f | %.2f | %.2f | %d | %d | %.3f | %.0f | %.0f | %.0f |\n",
			r.Config.Concurrency,
			r.Config.Behavior,
			r.Result.Timing.DrainSeconds,
			r.Result.Throughput.SuccessfulPerSecond,
			attemptsPerSecond,
			r.Result.Throughput.AttemptedPerSecond,
			r.Result.Throughput.SuccessfulTotal,
			r.Result.Throughput.FailedTotal,
			r.Result.Throughput.FailureRate,
			r.Result.HTTP.P50MS,
			r.Result.HTTP.P95MS,
			r.Result.HTTP.P99MS,
		))
	}

	// Only show the attempt breakdown when at least one run had
	// retries. Otherwise it is noise for purely successful matrices.
	hasRetries := false
	for _, r := range results {
		totalAttempts := int64(0)
		for _, row := range r.Result.AttemptBreakdown {
			totalAttempts += row.Delivered +
				row.HTTP5xx +
				row.HTTP4xx +
				row.OtherStatus +
				row.ConnectionErr +
				row.Timeout
		}
		if totalAttempts > r.Result.Throughput.SuccessfulTotal {
			hasRetries = true
			break
		}
	}

	if hasRetries {
		sb.WriteString("\n## Attempt outcomes by attempt number\n\n")
		sb.WriteString(
			"Each row shows how many attempts at that attempt number ended " +
				"with each outcome. A delivery can appear at multiple " +
				"attempt numbers if it retried.\n\n",
		)
		sb.WriteString(
			"| Concurrency | Behavior | Attempt # | Delivered | " +
				"HTTP 5xx | HTTP 4xx | Connection | Timeout | Other |\n",
		)
		sb.WriteString(
			"|---|---|---|---|---|---|---|---|---|\n",
		)

		for _, r := range results {
			for _, row := range r.Result.AttemptBreakdown {
				sb.WriteString(fmt.Sprintf(
					"| %d | %s | %d | %d | %d | %d | %d | %d | %d |\n",
					r.Config.Concurrency,
					r.Config.Behavior,
					row.AttemptNumber,
					row.Delivered,
					row.HTTP5xx,
					row.HTTP4xx,
					row.ConnectionErr,
					row.Timeout,
					row.OtherStatus,
				))
			}
		}
	}

	sb.WriteString("\n## Notes\n\n")
	sb.WriteString(
		"Throughput at low concurrency is bounded by " +
			"`PollInterval / Concurrency`. At higher concurrency, " +
			"the bottleneck shifts to the MongoDB claim rate and " +
			"downstream latency.\n",
	)

	// Defense in depth (issue #50): if any run had retries, the
	// attempt-breakdown section MUST be in the output. A regression
	// that accidentally removes the block above would otherwise write
	// a summary.md that looks correct except the section is simply
	// missing. Fail loudly instead.
	if hasRetries && !strings.Contains(sb.String(), "## Attempt outcomes by attempt number") {
		return fmt.Errorf(
			"matrix summary missing attempt breakdown section despite retries",
		)
	}

	path := filepath.Join(cfg.OutputDir, "summary.md")
	if err := os.WriteFile(
		path,
		[]byte(sb.String()),
		0o644,
	); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}
