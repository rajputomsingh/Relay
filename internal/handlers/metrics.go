package handlers

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const metricsTimeout = 5 * time.Second

var metricEventStatuses = []string{
	"pending",
	"processing",
	"delivered",
	"failed",
	"disabled",
}

var durationBucketLimitsMS = []int64{
	100,
	500,
	1000,
	5000,
	10000,
}

type metricsSnapshot struct {
	EventCounts          map[string]int64
	AttemptsTotal        int64
	RetriesTotal         int64
	DurationBucketCounts map[int64]int64
	DurationCount        int64
	DurationSumSeconds   float64
}

type metricsStore interface {
	Snapshot(context.Context) (metricsSnapshot, error)
}

type mongoMetricsStore struct {
	events   *mongo.Collection
	attempts *mongo.Collection
}

type MetricsHandler struct {
	store  metricsStore
	apiKey string
}

type durationAggregation struct {
	Count   int64   `bson:"count"`
	SumMS   float64 `bson:"sum_ms"`
	Le100   int64   `bson:"le_100"`
	Le500   int64   `bson:"le_500"`
	Le1000  int64   `bson:"le_1000"`
	Le5000  int64   `bson:"le_5000"`
	Le10000 int64   `bson:"le_10000"`
}

func NewMetricsHandler(
	eventsCollection *mongo.Collection,
	attemptsCollection *mongo.Collection,
	apiKey string,
) *MetricsHandler {
	return &MetricsHandler{
		store: &mongoMetricsStore{
			events:   eventsCollection,
			attempts: attemptsCollection,
		},
		apiKey: apiKey,
	}
}

func (h *MetricsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/metrics", h.serve)
}

func (h *MetricsHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), metricsTimeout)
	defer cancel()

	snapshot, err := h.store.Snapshot(ctx)
	if err != nil {
		// Do not expose database errors or internal details.
		http.Error(
			w,
			"metrics temporarily unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}

	w.Header().Set(
		"Content-Type",
		"text/plain; version=0.0.4; charset=utf-8",
	)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	writeMetrics(w, snapshot)
}

func (h *MetricsHandler) authorized(r *http.Request) bool {
	if h.apiKey == "" {
		return false
	}

	const bearerPrefix = "Bearer "

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, bearerPrefix) {
		return false
	}

	providedKey := strings.TrimPrefix(auth, bearerPrefix)
	if providedKey == "" {
		return false
	}

	return subtle.ConstantTimeCompare(
		[]byte(providedKey),
		[]byte(h.apiKey),
	) == 1
}

func (s *mongoMetricsStore) Snapshot(
	ctx context.Context,
) (metricsSnapshot, error) {
	snapshot := metricsSnapshot{
		EventCounts:          make(map[string]int64, len(metricEventStatuses)),
		DurationBucketCounts: make(map[int64]int64, len(durationBucketLimitsMS)),
	}

	for _, status := range metricEventStatuses {
		count, err := s.events.CountDocuments(
			ctx,
			bson.D{{Key: "delivery_status", Value: status}},
		)
		if err != nil {
			return metricsSnapshot{}, fmt.Errorf(
				"count events with status %q: %w",
				status,
				err,
			)
		}

		snapshot.EventCounts[status] = count
	}

	attemptsTotal, err := s.attempts.CountDocuments(ctx, bson.D{})
	if err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"count delivery attempts: %w",
			err,
		)
	}
	snapshot.AttemptsTotal = attemptsTotal

	retriesTotal, err := s.attempts.CountDocuments(
		ctx,
		bson.D{
			{
				Key: "attempt_number",
				Value: bson.D{
					{Key: "$gt", Value: 1},
				},
			},
		},
	)
	if err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"count delivery retries: %w",
			err,
		)
	}
	snapshot.RetriesTotal = retriesTotal

	groupFields := bson.D{
		{Key: "_id", Value: nil},
		{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		{
			Key:   "sum_ms",
			Value: bson.D{{Key: "$sum", Value: "$duration_ms"}},
		},
		{Key: "le_100", Value: bucketSum(100)},
		{Key: "le_500", Value: bucketSum(500)},
		{Key: "le_1000", Value: bucketSum(1000)},
		{Key: "le_5000", Value: bucketSum(5000)},
		{Key: "le_10000", Value: bucketSum(10000)},
	}

	cursor, err := s.attempts.Aggregate(
		ctx,
		mongo.Pipeline{
			bson.D{{Key: "$group", Value: groupFields}},
		},
	)
	if err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"aggregate delivery durations: %w",
			err,
		)
	}
	defer cursor.Close(ctx)

	if cursor.Next(ctx) {
		var result durationAggregation
		if err := cursor.Decode(&result); err != nil {
			return metricsSnapshot{}, fmt.Errorf(
				"decode delivery duration aggregation: %w",
				err,
			)
		}

		snapshot.DurationCount = result.Count
		snapshot.DurationSumSeconds = result.SumMS / 1000

		snapshot.DurationBucketCounts[100] = result.Le100
		snapshot.DurationBucketCounts[500] = result.Le500
		snapshot.DurationBucketCounts[1000] = result.Le1000
		snapshot.DurationBucketCounts[5000] = result.Le5000
		snapshot.DurationBucketCounts[10000] = result.Le10000
	}

	if err := cursor.Err(); err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"iterate delivery duration aggregation: %w",
			err,
		)
	}

	return snapshot, nil
}

func bucketSum(limitMS int64) bson.D {
	return bson.D{
		{
			Key: "$sum",
			Value: bson.D{
				{
					Key: "$cond",
					Value: bson.A{
						bson.D{
							{
								Key: "$lte",
								Value: bson.A{
									"$duration_ms",
									limitMS,
								},
							},
						},
						1,
						0,
					},
				},
			},
		},
	}
}

func writeMetrics(w http.ResponseWriter, snapshot metricsSnapshot) {
	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_events Number of events grouped by delivery status.",
	)
	_, _ = fmt.Fprintln(w, "# TYPE relay_events gauge")

	for _, status := range metricEventStatuses {
		_, _ = fmt.Fprintf(
			w,
			"relay_events{status=%s} %d\n",
			strconv.Quote(status),
			snapshot.EventCounts[status],
		)
	}

	// Snapshot counts can decrease if MongoDB records are deleted.
	// Therefore, attempt and retry metrics are gauges, not counters.
	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_delivery_attempts Number of persisted delivery attempt records.",
	)
	_, _ = fmt.Fprintln(w, "# TYPE relay_delivery_attempts gauge")
	_, _ = fmt.Fprintf(
		w,
		"relay_delivery_attempts %d\n",
		snapshot.AttemptsTotal,
	)

	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_delivery_retries Number of persisted attempts numbered greater than one.",
	)
	_, _ = fmt.Fprintln(w, "# TYPE relay_delivery_retries gauge")
	_, _ = fmt.Fprintf(
		w,
		"relay_delivery_retries %d\n",
		snapshot.RetriesTotal,
	)

	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_delivery_duration_seconds Duration of persisted webhook delivery attempts in seconds.",
	)
	_, _ = fmt.Fprintln(
		w,
		"# TYPE relay_delivery_duration_seconds histogram",
	)

	for _, limitMS := range durationBucketLimitsMS {
		_, _ = fmt.Fprintf(
			w,
			"relay_delivery_duration_seconds_bucket{le=%s} %d\n",
			strconv.Quote(strconv.FormatInt(limitMS, 10)),
			snapshot.DurationBucketCounts[limitMS],
		)
	}

	_, _ = fmt.Fprintf(
		w,
		"relay_delivery_duration_seconds_bucket{le=%s} %d\n",
		strconv.Quote("+Inf"),
		snapshot.DurationCount,
	)

	_, _ = fmt.Fprintf(
		w,
		"relay_delivery_duration_seconds_sum %g\n",
		snapshot.DurationSumSeconds,
	)
	_, _ = fmt.Fprintf(
		w,
		"relay_delivery_duration_seconds_count %d\n",
		snapshot.DurationCount,
	)
}
