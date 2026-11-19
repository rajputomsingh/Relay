package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newRecoveryWorker(db workerIntegrationDB, lease time.Duration) *Worker {
	return NewWorker(
		db.events, db.endpoints, db.deliveries, db.attempts,
		newTestAggregator(db),
		Config{
			PollInterval:   50 * time.Millisecond,
			RequestTimeout: 5 * time.Second,
			LeaseDuration:  lease,
			Concurrency:    1,
		},
	)
}

// Worker A claims a delivery and stalls mid-request. Its lease expires,
// worker B reclaims it with a new lease token and delivers successfully.
// When A finally returns with a 500, its write must be rejected and
// the delivered state written by B must stay unchanged.
func TestRecoveryIntegrationStaleWorkerCannotOverwriteReclaimedDelivery(t *testing.T) {
	db := newWorkerIntegrationDB(t)

	var requests atomic.Int32
	aArrived := make(chan struct{})
	releaseA := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseA) }) }

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if requests.Add(1) == 1 {
			close(aArrived)
			<-releaseA
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(release)

	eventID := insertWorkerIntegrationEvent(t, db.events)
	endpointID := insertWorkerIntegrationEndpoint(t, db.endpoints, server.URL, "", 3)
	deliveryID := insertWorkerIntegrationDelivery(t, db.deliveries, eventID, endpointID, 3)

	const lease = time.Second
	workerA := newRecoveryWorker(db, lease)
	workerB := newRecoveryWorker(db, lease)

	errA := make(chan error, 1)
	go func() { errA <- workerA.processOne(context.Background()) }()

	select {
	case <-aArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("worker A never reached the receiver")
	}

	tokenA := readWorkerDeliveryState(t, db.deliveries, deliveryID).LeaseToken
	if tokenA == "" {
		t.Fatal("worker A should hold a lease token")
	}

	time.Sleep(lease + 200*time.Millisecond)

	if err := workerB.processOne(context.Background()); err != nil {
		t.Fatalf("worker B reclaim: %v", err)
	}

	afterB := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	if afterB.Status != StatusDelivered {
		t.Fatalf("after B: status = %q; want delivered", afterB.Status)
	}

	release()

	var err error
	select {
	case err = <-errA:
	case <-time.After(5 * time.Second):
		t.Fatal("worker A never returned")
	}
	if err == nil || !strings.Contains(err.Error(), "no longer owned by this lease") {
		t.Fatalf("worker A err = %v; want 'no longer owned by this lease'", err)
	}

	final := readWorkerDeliveryState(t, db.deliveries, deliveryID)
	// The record must look exactly like B left it.
	if final.Status != StatusDelivered {
		t.Errorf("status = %q; want delivered", final.Status)
	}
	if final.Attempts != afterB.Attempts {
		t.Errorf("attempts = %d; want %d (stale worker must not bump it)", final.Attempts, afterB.Attempts)
	}
	if final.LastError != "" {
		t.Errorf("last_error = %q; want empty", final.LastError)
	}
	if final.DeliveredAt == nil || afterB.DeliveredAt == nil || !final.DeliveredAt.Equal(*afterB.DeliveredAt) {
		t.Errorf("delivered_at changed: before %v, after %v", afterB.DeliveredAt, final.DeliveredAt)
	}
	if final.NextAttemptAt != nil {
		t.Errorf("next_attempt_at = %v; want unset", final.NextAttemptAt)
	}
	if final.LeaseUntil != nil || final.LeaseToken != "" {
		t.Errorf("lease not cleared: until=%v token=%q", final.LeaseUntil, final.LeaseToken)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("webhook requests = %d; want 2", got)
	}
}
