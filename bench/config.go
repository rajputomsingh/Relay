// Package main implements Relay's delivery benchmark harness.
//
// The harness reuses the real delivery worker and MongoDB schema so that
// measured throughput reflects production behavior. It is not part of the
// server binary and is invoked via `go run ./bench`.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Behavior names a downstream server pattern used during a benchmark run.
type Behavior string

const (
	BehaviorFast        Behavior = "fast"
	BehaviorSlow        Behavior = "slow"
	BehaviorHTTP500     Behavior = "http-500"
	BehaviorTimeout     Behavior = "timeout"
	BehaviorTransient   Behavior = "transient-503"
	BehaviorUnavailable Behavior = "unavailable"
)

// allBehaviors is the canonical list of supported behaviors.
var allBehaviors = []Behavior{
	BehaviorFast,
	BehaviorSlow,
	BehaviorHTTP500,
	BehaviorTimeout,
	BehaviorTransient,
	BehaviorUnavailable,
}

// Config holds all tunable parameters for a single benchmark run.
type Config struct {
	// Concurrency is the delivery.Worker concurrency setting.
	Concurrency int

	// Behavior is the downstream pattern the mock server exposes.
	Behavior Behavior

	// Count is the number of deliveries seeded before the run.
	Count int

	// MaxAttempts is the delivery retry limit captured on each seeded
	// delivery. Defaults to 8 to match production config.
	MaxAttempts int

	// DrainTimeout bounds how long the harness waits for the worker to
	// drain the backlog. Failure scenarios with retries can take much
	// longer than successful runs.
	DrainTimeout time.Duration

	// MongoURI is the MongoDB connection string.
	MongoURI string

	// Database overrides the per-run database name. Empty means generate.
	Database string

	// OutputDir is where JSON results are written.
	OutputDir string

	// SlowDelay is the artificial latency the "slow" behavior adds to
	// each response.
	SlowDelay time.Duration

	// TransientFailures is how many of the initial requests to the
	// "transient-503" behavior fail before recovering.
	TransientFailures int

	// WarmupDeliveries are seeded and drained before the measured run
	// begins. Their results are discarded from the report.
	WarmupDeliveries int

	// Quiet suppresses per-tick progress lines. Errors and summaries
	// still print.
	Quiet bool

	// Matrix, when true, runs every supported behavior at every
	// configured concurrency level and writes a consolidated summary.
	Matrix bool

	// ConcurrencyLevels controls which concurrency values the matrix
	// runner exercises. Ignored when Matrix is false.
	ConcurrencyLevels []int
}

// ErrUsage is returned when the CLI arguments are invalid.
var ErrUsage = errors.New("invalid benchmark configuration")

// parseFlags reads CLI flags into a Config, applying defaults.
func parseFlags(args []string) (Config, error) {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		concurrency       int
		behavior          string
		count             int
		maxAttempts       int
		drainTimeout      time.Duration
		mongoURI          string
		database          string
		outputDir         string
		slowDelay         time.Duration
		transientFailures int
		warmupDeliveries  int
		quiet             bool
		matrix            bool
		concurrencyList   string
	)

	fs.IntVar(&concurrency, "concurrency", 4,
		"delivery worker concurrency (1-32)")
	fs.StringVar(&behavior, "behavior", string(BehaviorFast),
		"downstream behavior: fast, slow, http-500, timeout, transient-503, unavailable")
	fs.IntVar(&count, "count", 500,
		"number of deliveries seeded before the run")
	fs.IntVar(&maxAttempts, "max-attempts", 8,
		"delivery max attempts (1-20)")
	fs.DurationVar(&drainTimeout, "drain-timeout", 5*time.Minute,
		"maximum time to wait for the backlog to drain")
	fs.StringVar(&mongoURI, "mongo-uri", "",
		"MongoDB URI (defaults to MONGODB_TEST_URI)")
	fs.StringVar(&database, "database", "",
		"database name (default: generated per run)")
	fs.StringVar(&outputDir, "output", "bench/results",
		"directory for JSON results")
	fs.DurationVar(&slowDelay, "slow-delay", 100*time.Millisecond,
		"artificial latency for the slow behavior")
	fs.IntVar(&transientFailures, "transient-failures", 50,
		"number of initial failures before the transient-503 behavior recovers")
	fs.IntVar(&warmupDeliveries, "warmup", 50,
		"deliveries processed and discarded before measurement")
	fs.BoolVar(&quiet, "quiet", false,
		"suppress per-tick progress output")
	fs.BoolVar(&matrix, "matrix", false,
		"run every behavior across every concurrency level")
	fs.StringVar(&concurrencyList, "concurrency-levels", "1,4,8,16",
		"comma-separated concurrency levels for --matrix")

	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}

	cfg := Config{
		Concurrency:       concurrency,
		Behavior:          Behavior(strings.TrimSpace(behavior)),
		Count:             count,
		MaxAttempts:       maxAttempts,
		DrainTimeout:      drainTimeout,
		MongoURI:          strings.TrimSpace(mongoURI),
		Database:          strings.TrimSpace(database),
		OutputDir:         strings.TrimSpace(outputDir),
		SlowDelay:         slowDelay,
		TransientFailures: transientFailures,
		WarmupDeliveries:  warmupDeliveries,
		Quiet:             quiet,
		Matrix:            matrix,
		ConcurrencyLevels: parseConcurrencyLevels(concurrencyList),
	}

	if cfg.MongoURI == "" {
		cfg.MongoURI = strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	switch {
	case c.Concurrency < 1 || c.Concurrency > 32:
		return fmt.Errorf("%w: concurrency must be between 1 and 32", ErrUsage)
	case c.Count < 1:
		return fmt.Errorf("%w: count must be positive", ErrUsage)
	case c.MaxAttempts < 1 || c.MaxAttempts > 20:
		return fmt.Errorf(
			"%w: max-attempts must be between 1 and 20",
			ErrUsage,
		)
	case c.DrainTimeout <= 0:
		return fmt.Errorf("%w: drain-timeout must be positive", ErrUsage)
	case c.MongoURI == "":
		return fmt.Errorf(
			"%w: mongo-uri is required (set --mongo-uri or MONGODB_TEST_URI)",
			ErrUsage,
		)
	case c.OutputDir == "":
		return fmt.Errorf("%w: output directory must not be empty", ErrUsage)
	case c.SlowDelay < 0:
		return fmt.Errorf("%w: slow-delay must not be negative", ErrUsage)
	case c.TransientFailures < 0:
		return fmt.Errorf(
			"%w: transient-failures must not be negative",
			ErrUsage,
		)
	case c.WarmupDeliveries < 0:
		return fmt.Errorf("%w: warmup must not be negative", ErrUsage)
	}

	if !isValidBehavior(c.Behavior) {
		return fmt.Errorf(
			"%w: behavior %q not recognized (want one of %s)",
			ErrUsage,
			c.Behavior,
			behaviorList(),
		)
	}

	if c.Matrix && len(c.ConcurrencyLevels) == 0 {
		return fmt.Errorf(
			"%w: --matrix requires at least one valid concurrency level",
			ErrUsage,
		)
	}

	return nil
}

func isValidBehavior(b Behavior) bool {
	for _, known := range allBehaviors {
		if b == known {
			return true
		}
	}
	return false
}

func behaviorList() string {
	names := make([]string, 0, len(allBehaviors))
	for _, b := range allBehaviors {
		names = append(names, string(b))
	}
	return strings.Join(names, ", ")
}

// parseConcurrencyLevels parses "1,4,8,16" into a slice of ints.
// Invalid entries are silently skipped; validate() rejects an empty
// result when the matrix flag is set.
func parseConcurrencyLevels(raw string) []int {
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if v < 1 || v > 32 {
			continue
		}
		out = append(out, v)
	}
	return out
}
