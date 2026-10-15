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
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const metricsTimeout = 5 * time.Second

// Bounded set of delivery statuses. Must stay in sync with the
// constants defined in internal/delivery/delivery.go.
var metricDeliveryStatuses = []string{
	"pending",
	"processing",
	"delivered",
	"failed",
}

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
	// Legacy event-level metrics. Kept for backward compatibility
	// and for operators still tracking legacy events.
	EventCounts map[string]int64

	// Delivery-level metrics. The primary signal for the
	// multi-endpoint delivery pipeline.
	DeliveryCounts         map[string]int64
	DeliveryEndpointCounts map[string]map[string]int64 // endpoint_id -> status -> count
	OldestPendingSeconds   float64
	OldestPendingKnown     bool

	AttemptsTotal        int64
	RetriesTotal         int64
	TerminalFailures     int64
	DurationBucketCounts map[int64]int64
	DurationCount        int64
	DurationSumSeconds   float64
}

type metricsStore interface {
	Snapshot(context.Context) (metricsSnapshot, error)
}

type mongoMetricsStore struct {
	events     *mongo.Collection
	attempts   *mongo.Collection
	deliveries *mongo.Collection
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

type endpointStatusCount struct {
	EndpointID string `bson:"endpoint_id"`
	Status     string `bson:"status"`
	Count      int64  `bson:"count"`
}

func NewMetricsHandler(
	eventsCollection *mongo.Collection,
	attemptsCollection *mongo.Collection,
	deliveriesCollection *mongo.Collection,
	apiKey string,
) *MetricsHandler {
	return &MetricsHandler{
		store: &mongoMetricsStore{
			events:     eventsCollection,
			attempts:   attemptsCollection,
			deliveries: deliveriesCollection,
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
		EventCounts: make(
			map[string]int64, len(metricEventStatuses),
		),
		DeliveryCounts: make(
			map[string]int64, len(metricDeliveryStatuses),
		),
		DeliveryEndpointCounts: make(
			map[string]map[string]int64,
		),
		DurationBucketCounts: make(
			map[int64]int64, len(durationBucketLimitsMS),
		),
	}

	// -----------------------------------------------------------------
	// Legacy event-level metrics (kept for backward compatibility).
	// -----------------------------------------------------------------

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

	// -----------------------------------------------------------------
	// Delivery-level metrics. Primary source of truth after
	// multi-endpoint delivery moved lifecycle state onto the
	// deliveries collection.
	// -----------------------------------------------------------------

	for _, status := range metricDeliveryStatuses {
		count, err := s.deliveries.CountDocuments(
			ctx,
			bson.D{{Key: "status", Value: status}},
		)
		if err != nil {
			return metricsSnapshot{}, fmt.Errorf(
				"count deliveries with status %q: %w",
				status,
				err,
			)
		}

		snapshot.DeliveryCounts[status] = count
	}

	// Per-endpoint status counts. Grouped so one round-trip returns
	// every endpoint/status combination. Cardinality is bounded by
	// (number of endpoints × number of statuses).
	cursor, err := s.deliveries.Aggregate(
		ctx,
		mongo.Pipeline{
			bson.D{
				{
					Key: "$group",
					Value: bson.D{
						{
							Key: "_id",
							Value: bson.D{
								{Key: "endpoint_id", Value: "$endpoint_id"},
								{Key: "status", Value: "$status"},
							},
						},
						{
							Key: "count",
							Value: bson.D{
								{Key: "$sum", Value: 1},
							},
						},
					},
				},
			},
			bson.D{
				{
					Key: "$project",
					Value: bson.D{
						{Key: "_id", Value: 0},
						{
							Key: "endpoint_id",
							Value: bson.D{
								{
									Key:   "$toString",
									Value: "$_id.endpoint_id",
								},
							},
						},
						{Key: "status", Value: "$_id.status"},
						{Key: "count", Value: 1},
					},
				},
			},
		},
	)
	if err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"aggregate deliveries by endpoint: %w",
			err,
		)
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var row endpointStatusCount
		if err := cursor.Decode(&row); err != nil {
			return metricsSnapshot{}, fmt.Errorf(
				"decode endpoint delivery aggregate: %w",
				err,
			)
		}

		if snapshot.DeliveryEndpointCounts[row.EndpointID] == nil {
			snapshot.DeliveryEndpointCounts[row.EndpointID] =
				make(map[string]int64)
		}
		snapshot.DeliveryEndpointCounts[row.EndpointID][row.Status] =
			row.Count
	}
	if err := cursor.Err(); err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"iterate endpoint delivery aggregate: %w",
			err,
		)
	}

	// Oldest due pending delivery age, in seconds. Only meaningful
	// when at least one pending delivery exists.
	var oldest bson.M
	err = s.deliveries.FindOne(
		ctx,
		bson.D{{Key: "status", Value: "pending"}},
		options.FindOne().
			SetSort(bson.D{{Key: "created_at", Value: 1}}).
			SetProjection(bson.D{{Key: "created_at", Value: 1}}),
	).Decode(&oldest)
	if err == nil {
		if createdAt, ok := oldest["created_at"].(bson.DateTime); ok {
			snapshot.OldestPendingKnown = true
			snapshot.OldestPendingSeconds = time.Since(
				createdAt.Time(),
			).Seconds()
		}
	} else if err != mongo.ErrNoDocuments {
		return metricsSnapshot{}, fmt.Errorf(
			"find oldest pending delivery: %w",
			err,
		)
	}

	// -----------------------------------------------------------------
	// Attempt-history metrics (unchanged).
	// -----------------------------------------------------------------

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

	// Terminal failures: deliveries that ended in a failed state.
	// Distinct from total failed attempts because retries can
	// accumulate before reaching a terminal failure.
	terminalFailures, err := s.deliveries.CountDocuments(
		ctx,
		bson.D{{Key: "status", Value: "failed"}},
	)
	if err != nil {
		return metricsSnapshot{}, fmt.Errorf(
			"count terminal delivery failures: %w",
			err,
		)
	}
	snapshot.TerminalFailures = terminalFailures

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

	durCursor, err := s.attempts.Aggregate(
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
	defer durCursor.Close(ctx)

	if durCursor.Next(ctx) {
		var result durationAggregation
		if err := durCursor.Decode(&result); err != nil {
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

	if err := durCursor.Err(); err != nil {
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
	// -----------------------------------------------------------------
	// Delivery-level metrics. Primary signal.
	// -----------------------------------------------------------------

	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_deliveries Number of delivery records grouped by status.",
	)
	_, _ = fmt.Fprintln(w, "# TYPE relay_deliveries gauge")

	for _, status := range metricDeliveryStatuses {
		_, _ = fmt.Fprintf(
			w,
			"relay_deliveries{status=%s} %d\n",
			strconv.Quote(status),
			snapshot.DeliveryCounts[status],
		)
	}

	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_deliveries_per_endpoint Number of delivery records grouped by endpoint and status.",
	)
	_, _ = fmt.Fprintln(w, "# TYPE relay_deliveries_per_endpoint gauge")

	for endpointID, statuses := range snapshot.DeliveryEndpointCounts {
		for _, status := range metricDeliveryStatuses {
			_, _ = fmt.Fprintf(
				w,
				"relay_deliveries_per_endpoint{endpoint_id=%s,status=%s} %d\n",
				strconv.Quote(endpointID),
				strconv.Quote(status),
				statuses[status],
			)
		}
	}

	if snapshot.OldestPendingKnown {
		_, _ = fmt.Fprintln(
			w,
			"# HELP relay_deliveries_oldest_pending_seconds Age in seconds of the oldest pending delivery.",
		)
		_, _ = fmt.Fprintln(
			w,
			"# TYPE relay_deliveries_oldest_pending_seconds gauge",
		)
		_, _ = fmt.Fprintf(
			w,
			"relay_deliveries_oldest_pending_seconds %g\n",
			snapshot.OldestPendingSeconds,
		)
	}

	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_deliveries_terminal_failures Number of deliveries that reached the failed state.",
	)
	_, _ = fmt.Fprintln(
		w,
		"# TYPE relay_deliveries_terminal_failures gauge",
	)
	_, _ = fmt.Fprintf(
		w,
		"relay_deliveries_terminal_failures %d\n",
		snapshot.TerminalFailures,
	)

	// -----------------------------------------------------------------
	// Legacy event-level metrics.
	// -----------------------------------------------------------------

	_, _ = fmt.Fprintln(
		w,
		"# HELP relay_events Number of events grouped by delivery status (legacy).",
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

	// -----------------------------------------------------------------
	// Attempt-history metrics.
	// -----------------------------------------------------------------

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
