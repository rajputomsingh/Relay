package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// writeJSONResult writes one run's results to the output directory.
func writeJSONResult(
	cfg Config,
	result runResult,
) (string, error) {
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir output: %w", err)
	}

	name := fmt.Sprintf(
		"%d_%s_%s.json",
		cfg.Concurrency,
		cfg.Behavior,
		time.Now().UTC().Format("20060102T150405Z"),
	)
	path := filepath.Join(cfg.OutputDir, name)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write result: %w", err)
	}

	return path, nil
}

// summarize prints a one-screen summary of a run.
func summarize(result runResult) {
	fmt.Println()
	fmt.Printf(
		"concurrency=%d behavior=%s count=%d\n",
		result.Config.Concurrency,
		result.Config.Behavior,
		result.Config.Count,
	)
	fmt.Printf(
		"drain=%.1fs  throughput=%.1f/s  attempted=%.1f/s\n",
		result.Timing.DrainSeconds,
		result.Throughput.SuccessfulPerSecond,
		result.Throughput.AttemptedPerSecond,
	)
	fmt.Printf(
		"delivered=%d failed=%d pending=%d  failure_rate=%.3f\n",
		result.Throughput.SuccessfulTotal,
		result.Throughput.FailedTotal,
		result.Throughput.PendingRemaining,
		result.Throughput.FailureRate,
	)
	fmt.Printf(
		"http   p50=%.0fms p95=%.0fms p99=%.0fms  requests=%d\n",
		result.HTTP.P50MS,
		result.HTTP.P95MS,
		result.HTTP.P99MS,
		result.HTTP.Requests,
	)
	fmt.Printf(
		"attempt p50=%.0fms p95=%.0fms p99=%.0fms  count=%d\n",
		result.Attempts.DurationP50MS,
		result.Attempts.DurationP95MS,
		result.Attempts.DurationP99MS,
		result.Attempts.Count,
	)
	fmt.Printf(
		"queue_to_first  p50=%.0fms p95=%.0fms p99=%.0fms\n",
		result.Queue.ToFirstAttemptP50MS,
		result.Queue.ToFirstAttemptP95MS,
		result.Queue.ToFirstAttemptP99MS,
	)
	fmt.Printf(
		"queue_to_delivered p50=%.0fms p95=%.0fms p99=%.0fms\n",
		result.Queue.ToDeliveredP50MS,
		result.Queue.ToDeliveredP95MS,
		result.Queue.ToDeliveredP99MS,
	)
	fmt.Println()
}