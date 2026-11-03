package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteMatrixSummaryRendersAttemptBreakdown proves the
// "## Attempt outcomes by attempt number" section appears whenever
// at least one run had retries. Regression guard for issue #50 — a
// non-idempotent replace script silently removed this block once,
// and no test caught it, so an 11-minute matrix run produced a
// summary.md that looked correct except the section was gone.
func TestWriteMatrixSummaryRendersAttemptBreakdown(t *testing.T) {
	cfg := Config{OutputDir: t.TempDir()}

	results := []matrixResult{
		{
			Config: Config{Concurrency: 2, Behavior: Behavior("http-500")},
			Result: runResult{
				Throughput: throughputResult{
					SuccessfulTotal: 0,
					FailedTotal:     20,
				},
				AttemptBreakdown: []attemptOutcomeRow{
					{AttemptNumber: 1, HTTP5xx: 20},
					{AttemptNumber: 2, HTTP5xx: 20},
				},
			},
		},
	}

	if err := writeMatrixSummary(cfg, results); err != nil {
		t.Fatalf("writeMatrixSummary: %v", err)
	}

	body := readSummary(t, cfg)

	mustContain(t, body, "## Attempt outcomes by attempt number",
		"attempt breakdown section must render when retries occurred")
	mustContain(t, body, "| 2 | http-500 | 1 | 0 | 20 |",
		"attempt #1 row must appear with 20 http_5xx")
	mustContain(t, body, "| 2 | http-500 | 2 | 0 | 20 |",
		"attempt #2 row must appear with 20 http_5xx")
}

// TestWriteMatrixSummaryOmitsAttemptBreakdownWhenNoRetries proves the
// section is suppressed on purely successful matrices, so fast/slow
// runs keep their summary.md clean.
func TestWriteMatrixSummaryOmitsAttemptBreakdownWhenNoRetries(t *testing.T) {
	cfg := Config{OutputDir: t.TempDir()}

	results := []matrixResult{
		{
			Config: Config{Concurrency: 4, Behavior: Behavior("fast")},
			Result: runResult{
				Throughput: throughputResult{SuccessfulTotal: 500},
				AttemptBreakdown: []attemptOutcomeRow{
					{AttemptNumber: 1, Delivered: 500},
				},
			},
		},
	}

	if err := writeMatrixSummary(cfg, results); err != nil {
		t.Fatalf("writeMatrixSummary: %v", err)
	}

	body := readSummary(t, cfg)

	if strings.Contains(body, "## Attempt outcomes by attempt number") {
		t.Fatalf("breakdown section must be omitted when no retries; got:\n%s", body)
	}
}

// TestWriteMatrixSummaryBreakdownIncludesEveryOutcomeClass asserts
// each classified outcome column is rendered in the documented order
// for a mixed-outcome run, so future column-shuffles fail loudly.
//
// Column order in the row matches the header:
//
//	| Concurrency | Behavior | Attempt # | Delivered | HTTP 5xx |
//	  HTTP 4xx | Connection | Timeout | Other |
func TestWriteMatrixSummaryBreakdownIncludesEveryOutcomeClass(t *testing.T) {
	cfg := Config{OutputDir: t.TempDir()}

	results := []matrixResult{
		{
			Config: Config{Concurrency: 8, Behavior: Behavior("transient-503")},
			Result: runResult{
				Throughput: throughputResult{SuccessfulTotal: 10},
				AttemptBreakdown: []attemptOutcomeRow{
					{
						AttemptNumber: 1,
						Delivered:     10,
						HTTP5xx:       10,
						HTTP4xx:       2,
						ConnectionErr: 1,
						Timeout:       3,
						OtherStatus:   4,
					},
				},
			},
		},
	}

	if err := writeMatrixSummary(cfg, results); err != nil {
		t.Fatalf("writeMatrixSummary: %v", err)
	}

	body := readSummary(t, cfg)

	mustContain(t, body, "| 8 | transient-503 | 1 | 10 | 10 | 2 | 1 | 3 | 4 |",
		"mixed-outcome row must render every column in order")
}

// TestMatrixBehaviorsDefaultSweepCoversRetryBehaviors guards the
// root cause of issue #50's detour: when --behavior is left at its
// default, the matrix sweep must include at least one retry-producing
// behavior, otherwise the attempt breakdown never renders in a full
// matrix run.
func TestMatrixBehaviorsDefaultSweepCoversRetryBehaviors(t *testing.T) {
	cfg := Config{Behavior: BehaviorFast}
	got := matrixBehaviors(cfg)

	if len(got) < 2 {
		t.Fatalf("default sweep should include multiple behaviors; got %v", got)
	}

	// Every retrying behavior should be in the default sweep so the
	// breakdown table is exercised on every full matrix run.
	retrying := []Behavior{
		BehaviorHTTP500,
		BehaviorTimeout,
		BehaviorTransient,
		BehaviorOutage,
		BehaviorUnavailable,
	}
	for _, want := range retrying {
		found := false
		for _, b := range got {
			if b == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default sweep missing retrying behavior %q; got %v", want, got)
		}
	}
}

// TestMatrixBehaviorsOverrideNarrowsSweep proves an explicit
// --behavior still narrows the sweep to one behavior.
func TestMatrixBehaviorsOverrideNarrowsSweep(t *testing.T) {
	cfg := Config{Behavior: BehaviorHTTP500}
	got := matrixBehaviors(cfg)

	if len(got) != 1 || got[0] != BehaviorHTTP500 {
		t.Fatalf("explicit behavior should narrow to one; got %v", got)
	}
}

// readSummary reads the summary.md written by writeMatrixSummary.
func readSummary(t *testing.T, cfg Config) string {
	t.Helper()
	path := filepath.Join(cfg.OutputDir, "summary.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary at %s: %v", path, err)
	}
	return string(data)
}

// mustContain fails with a readable diff if needle is not in haystack.
func mustContain(t *testing.T, haystack, needle, reason string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s\nwanted: %q\ngot:\n%s", reason, needle, haystack)
	}
}
