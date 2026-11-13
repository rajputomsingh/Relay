package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEndpointHealthIntegrationLatencies(t *testing.T) {
	db, _ := newEndpointHealthIntegration(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-time.Hour)
	epStore := endpoints.NewStore(db.Collection("endpoints"))
	handler := NewEndpointHealthHandler(epStore, db.Collection("deliveries"), db.Collection("delivery_attempts"), testAPIKey)
	handler.now = func() time.Time { return now }
	mux := http.NewServeMux()
	handler.Register(mux)

	distribution := make([]int64, 100)
	for i := range distribution {
		distribution[i] = int64(i + 1)
	}
	tests := []struct {
		name          string
		latencies     []int64
		failures      int
		unknown       int
		atCutoff      bool
		missingFinish bool
		want          []float64
		tolerance     float64
	}{
		{name: "known distribution excludes failed and unknown outcomes", latencies: distribution, failures: 2, unknown: 1, want: []float64{50, 95, 99}, tolerance: 1},
		{name: "zero latency is a number", latencies: []int64{0}, want: []float64{0, 0, 0}},
		{name: "single successful attempt", latencies: []int64{37}, want: []float64{37, 37, 37}},
		{name: "window cutoff is inclusive", latencies: []int64{7}, atCutoff: true, want: []float64{7, 7, 7}},
		{name: "failures only", failures: 2},
		{name: "no attempts in window"},
		{name: "missing finish is not a zero latency", missingFinish: true},
		{name: "missing finish does not dilute valid samples", latencies: []int64{37}, missingFinish: true, want: []float64{37, 37, 37}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep, err := epStore.Create(ctx, endpoints.Endpoint{Name: tt.name, URL: "https://example.com/webhook", MaxAttempts: 3})
			if err != nil {
				t.Fatalf("create endpoint: %v", err)
			}
			started := now.Add(-time.Minute)
			if tt.atCutoff {
				started = cutoff
			}
			attempt := func(endpointID bson.ObjectID, at time.Time, latency int64, outcome string) delivery.Attempt {
				return delivery.Attempt{
					EventID: bson.NewObjectID(), EndpointID: endpointID, AttemptNumber: 1,
					StartedAt: at, FinishedAt: at.Add(time.Duration(latency) * time.Millisecond),
					// Deliberately inconsistent: the API contract uses timestamps.
					DurationMS: 999999, Outcome: outcome,
				}
			}
			docs := []any{
				attempt(ep.ID, cutoff.Add(-time.Millisecond), 900000, attemptOutcomeDelivered),
				attempt(bson.NewObjectID(), started, 900000, attemptOutcomeDelivered),
			}
			for _, latency := range tt.latencies {
				docs = append(docs, attempt(ep.ID, started, latency, attemptOutcomeDelivered))
			}
			for i := 0; i < tt.failures; i++ {
				docs = append(docs, attempt(ep.ID, started, 900000, attemptOutcomeFailed))
			}
			for i := 0; i < tt.unknown; i++ {
				docs = append(docs, attempt(ep.ID, started, 900000, "unknown"))
			}
			successes := len(tt.latencies)
			if tt.missingFinish {
				docs = append(docs, bson.M{"endpoint_id": ep.ID, "started_at": started, "outcome": attemptOutcomeDelivered})
				successes++
			}
			if _, err := db.Collection("delivery_attempts").InsertMany(ctx, docs); err != nil {
				t.Fatalf("insert attempts: %v", err)
			}

			rec := performEndpointRequest(mux, http.MethodGet, "/v1/endpoints/"+ep.ID.Hex()+"/health", "", testAPIKey)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			var previous float64
			for i, key := range []string{"p50_latency_ms", "p95_latency_ms", "p99_latency_ms"} {
				raw, ok := body[key]
				if !ok {
					t.Fatalf("missing %s in %s", key, rec.Body.String())
				}
				if tt.want == nil {
					if string(raw) != "null" {
						t.Fatalf("%s = %s, want null", key, raw)
					}
					continue
				}
				var got *float64
				if err := json.Unmarshal(raw, &got); err != nil || got == nil {
					t.Fatalf("%s = %s, want a number (decode error: %v)", key, raw, err)
				}
				// MongoDB uses approximate percentiles. Allow one sample's spacing
				// for this known distribution, rather than requiring an exact rank.
				if math.Abs(*got-tt.want[i]) > tt.tolerance {
					t.Fatalf("%s = %v, want %v +/- %v", key, *got, tt.want[i], tt.tolerance)
				}
				if i > 0 && *got < previous {
					t.Fatalf("percentiles are not ordered: %s", rec.Body.String())
				}
				previous = *got
			}
			var stats endpointHealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
				t.Fatalf("decode counts: %v", err)
			}
			attempts := successes + tt.failures + tt.unknown
			if stats.Attempts1h != int64(attempts) || stats.Successes1h != int64(successes) || stats.Failures1h != int64(tt.failures) {
				t.Fatalf("existing counts changed: %+v", stats)
			}
			var rate float64
			if attempts > 0 {
				rate = float64(successes) / float64(attempts) * 100
			}
			if stats.SuccessRate1h != rate {
				t.Fatalf("success rate = %v, want %v", stats.SuccessRate1h, rate)
			}
		})
	}
}
