package delivery

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// deliveryDuration tracks outbound HTTP request latency.
	deliveryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "relay",
			Subsystem: "delivery",
			Name:      "duration_seconds",
			Help:      "Duration of outbound webhook HTTP requests.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"endpoint_id"},
	)

	// deliveryAttempts tracks the result of each attempt.
	deliveryAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "relay",
			Subsystem: "delivery",
			Name:      "attempts_total",
			Help:      "Total number of delivery attempts, grouped by outcome.",
		},
		[]string{"endpoint_id", "outcome"},
	)
)
