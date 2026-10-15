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

// matrixBehaviors selects which behaviors the matrix runner uses. When
// the caller specified a non-default behavior, only that one is used.
// Otherwise, fast and slow — the two behaviors that produce meaningful
// throughput numbers without retry inflation.
func matrixBehaviors(cfg Config) []Behavior {
	if cfg.Behavior != BehaviorFast {
		return []Behavior{cfg.Behavior}
	}
	return []Behavior{BehaviorFast, BehaviorSlow}
}

// runSingle executes one benchmark combination with a fresh database.
func runSingle(
	ctx context.Context,
	cfg Config,
	client *mongo.Client,
	logger *slog.Logger,
) (runResult, error) {
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
		"Attempted (req/s) | Delivered | Failed | Failure Rate | ",
	)
	sb.WriteString("HTTP p50 (ms) | HTTP p95 (ms) | HTTP p99 (ms) |\n")
	sb.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")

	for _, r := range results {
		sb.WriteString(fmt.Sprintf(
			"| %d | %s | %.2f | %.2f | %.2f | %d | %d | %.3f | %.0f | %.0f | %.0f |\n",
			r.Config.Concurrency,
			r.Config.Behavior,
			r.Result.Timing.DrainSeconds,
			r.Result.Throughput.SuccessfulPerSecond,
			r.Result.Throughput.AttemptedPerSecond,
			r.Result.Throughput.SuccessfulTotal,
			r.Result.Throughput.FailedTotal,
			r.Result.Throughput.FailureRate,
			r.Result.HTTP.P50MS,
			r.Result.HTTP.P95MS,
			r.Result.HTTP.P99MS,
		))
	}

	sb.WriteString("\n## Notes\n\n")
	sb.WriteString(
		"Throughput at low concurrency is bounded by " +
			"`PollInterval / Concurrency`. At higher concurrency, " +
			"the bottleneck shifts to the MongoDB claim rate and " +
			"downstream latency.\n",
	)

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
