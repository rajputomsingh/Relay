package reconciler

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	eventsHealed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "relay_reconciler_events_healed_total",
		Help: "Total number of orphaned events healed by the reconciler",
	})

	deliveriesReset = promauto.NewCounter(prometheus.CounterOpts{
		Name: "relay_reconciler_deliveries_reset_total",
		Help: "Total number of stuck deliveries reset to pending by the reconciler",
	})

	fanoutRecovered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "relay_reconciler_fanout_recovered_total",
		Help: "Total number of incomplete fan-out events recovered by the reconciler",
	})

	reconcileDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "relay_reconciler_duration_seconds",
		Help:    "Time taken for a single reconcile pass",
		Buckets: prometheus.DefBuckets,
	})
)

// EventFanouter is the interface the reconciler uses to re-trigger fan-out.
// This allows us to inject the existing matching logic without circular imports.
type EventFanouter interface {
	FanOutEvent(ctx context.Context, eventID bson.ObjectID, source, eventType string) error
}

// Config holds the tunable parameters for the reconciler.
type Config struct {
	Interval        time.Duration // How often the loop runs (e.g., 60s)
	OrphanThreshold time.Duration // How old an event must be to be considered orphaned (e.g., 5m)
	// Deprecated: lease recovery now uses delivery.lease_until directly.
	// Retained for backward compatibility with existing callers; has no effect.
	StuckThreshold time.Duration
}

// DefaultConfig provides safe defaults.
var DefaultConfig = Config{
	Interval:        60 * time.Second,
	OrphanThreshold: 5 * time.Minute,
	StuckThreshold:  2 * time.Minute,
}

// Reconciler is the background worker that heals the database.
type Reconciler struct {
	store    *Store
	fanouter EventFanouter
	config   Config
	logger   *slog.Logger
}

// New creates a new Reconciler.
func New(store *Store, fanouter EventFanouter, cfg Config, logger *slog.Logger) *Reconciler {
	return &Reconciler{
		store:    store,
		fanouter: fanouter,
		config:   cfg,
		logger:   logger,
	}
}

// Run starts the reconciler loop. It blocks until the context is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.logger.Info("reconciler started",
		"interval", r.config.Interval,
		"orphan_threshold", r.config.OrphanThreshold,
	)

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	// Run immediately on startup, then wait for the ticker.
	r.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler stopping")
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

// reconcile performs one full healing pass.
func (r *Reconciler) reconcile(ctx context.Context) {
	start := time.Now()
	defer func() {
		reconcileDuration.Observe(time.Since(start).Seconds())
	}()

	// 1. Heal expired delivery leases.
	resetCount, err := r.store.ResetExpiredLeases(ctx, time.Now().UTC())
	if err != nil {
		r.logger.Error("failed to reset expired delivery leases", "error", err)
	} else if resetCount > 0 {
		r.logger.Info("reset expired delivery leases to pending", "count", resetCount)
		deliveriesReset.Add(float64(resetCount))
	}

	// 2. Heal incomplete fan-out.
	fanoutThreshold := time.Now().UTC().Add(-r.config.OrphanThreshold)
	incompleteFanouts, err := r.store.FindIncompleteFanout(ctx, fanoutThreshold)
	if err != nil {
		r.logger.Error("failed to find incomplete fanout events", "error", err)
	} else if len(incompleteFanouts) > 0 {
		r.logger.Info(
			"found incomplete fanout events, re-triggering fan-out",
			"count", len(incompleteFanouts),
		)
		for _, evt := range incompleteFanouts {
			if err := r.fanouter.FanOutEvent(ctx, evt.ID, evt.Source, evt.EventType); err != nil {
				r.logger.Error(
					"failed to re-fan-out incomplete event",
					"event_id", evt.ID.Hex(),
					"error", err,
				)
			} else {
				fanoutRecovered.Inc()
			}
		}
	}

	// 3. Heal orphaned events (received_at < threshold, no deliveries, no
	// fanout_status). These are events where ingestion succeeded but the
	// fan-out step never ran.
	orphanThreshold := time.Now().UTC().Add(-r.config.OrphanThreshold)
	orphanedEvents, err := r.store.FindOrphanedEvents(ctx, orphanThreshold)
	if err != nil {
		r.logger.Error("failed to find orphaned events", "error", err)
	} else if len(orphanedEvents) > 0 {
		r.logger.Info("found orphaned events, triggering fan-out", "count", len(orphanedEvents))

		for _, evt := range orphanedEvents {
			if err := r.fanouter.FanOutEvent(ctx, evt.ID, evt.Source, evt.EventType); err != nil {
				r.logger.Error("failed to fan-out orphaned event", "event_id", evt.ID.Hex(), "error", err)
			} else {
				eventsHealed.Inc()
			}
		}
	}

	r.logger.Debug("reconcile pass complete", "duration_ms", time.Since(start).Milliseconds())
}
