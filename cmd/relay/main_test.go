package main

import (
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/config"
)

// The delivery worker rejects a non-positive poll interval, so the
// wiring from config to worker must always produce a positive duration.
// Regression guard for #96: cmd/relay once dropped PollInterval and
// LeaseDuration when constructing delivery.Config, which left the worker
// unable to start.
func TestWorkerDeliveryConfigProducesValidWorkerConfig(t *testing.T) {
	tests := []struct {
		name         string
		pollSeconds  int
		concurrency  int
		wantInterval time.Duration
	}{
		{name: "default poll seconds", pollSeconds: 2, concurrency: 4, wantInterval: 2 * time.Second},
		{name: "explicit poll seconds", pollSeconds: 5, concurrency: 4, wantInterval: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				WorkerPollSeconds: tt.pollSeconds,
				WorkerConcurrency: tt.concurrency,
			}

			got := workerDeliveryConfig(cfg)

			if got.PollInterval != tt.wantInterval {
				t.Errorf(
					"PollInterval = %v, want %v",
					got.PollInterval,
					tt.wantInterval,
				)
			}
			if got.PollInterval <= 0 {
				t.Errorf("PollInterval = %v, want positive", got.PollInterval)
			}
			if got.LeaseDuration <= got.RequestTimeout {
				t.Errorf(
					"LeaseDuration = %v, want greater than RequestTimeout %v",
					got.LeaseDuration,
					got.RequestTimeout,
				)
			}
			if got.Concurrency != tt.concurrency {
				t.Errorf("Concurrency = %d, want %d", got.Concurrency, tt.concurrency)
			}
		})
	}
}
