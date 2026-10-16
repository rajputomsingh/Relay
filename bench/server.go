package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"
)

// mockServer is an HTTP server whose behavior is chosen at construction.
//
// It records per-request handler durations so the benchmark can report
// HTTP latency as measured from the server side. That measures time
// spent handling the request, not the client-observed network round
// trip.
type mockServer struct {
	*httptest.Server

	behavior          Behavior
	slowDelay         time.Duration
	transientFailures int

	// outageDuration is the configured outage length for BehaviorOutage.
	// outageUntil is when the mock begins serving successful responses
	// again. Both are zero for other behaviors.
	outageDuration time.Duration
	outageUntil    time.Time

	requestCount  atomic.Int64
	successCount  atomic.Int64
	failureCount  atomic.Int64
	transientSeen atomic.Int64

	// durations collects handler durations in milliseconds. Protected
	// by mu because multiple worker goroutines may append concurrently.
	mu        sync.Mutex
	durations []int64
}

// mockServerSnapshot is a lock-free snapshot of the server's counters.
type mockServerSnapshot struct {
	Requests      int64
	Successes     int64
	Failures      int64
	TransientHits int64
}

// newMockServer starts a mock webhook server for the given behavior.
//
// The outageDuration parameter is only used when behavior is
// BehaviorOutage. The outage clock starts at construction time; for
// benchmark runs this coincides with the start of the measured window,
// because the harness constructs the mock immediately before seeding.
func newMockServer(
	behavior Behavior,
	slowDelay time.Duration,
	transientFailures int,
	outageDuration time.Duration,
) *mockServer {
	ms := &mockServer{
		behavior:          behavior,
		slowDelay:         slowDelay,
		transientFailures: transientFailures,
		outageDuration:    outageDuration,
	}

	if behavior == BehaviorOutage && outageDuration > 0 {
		ms.outageUntil = time.Now().Add(outageDuration)
	}

	if behavior == BehaviorUnavailable {
		// Do not bind a listener. Port 1 is reserved on Windows and
		// privileged on Unix, so connections to it are refused
		// immediately without TIME_WAIT delays.
		//
		// Binding a real listener and immediately closing it leaves
		// the port in TIME_WAIT on Windows, which causes connect
		// attempts to hang waiting for the TCP handshake instead of
		// failing fast. URL() returns the port-1 fallback when
		// Server is nil.
		return ms
	}

	ms.Server = httptest.NewServer(http.HandlerFunc(ms.handle))
	return ms
}

func (s *mockServer) handle(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	clientCanceled := false

	defer func() {
		// Do not record a handler duration for requests that the
		// client canceled. The handler may have slept past the
		// client's timeout while waiting for context cancellation
		// to propagate; that duration reflects the mock's internal
		// sleep, not the client-observed request latency. Counting
		// it would make http.p99 look like the sleep duration
		// instead of the actual request timeout.
		if clientCanceled {
			return
		}
		elapsed := time.Since(started).Milliseconds()
		s.mu.Lock()
		s.durations = append(s.durations, elapsed)
		s.mu.Unlock()
	}()

	s.requestCount.Add(1)

	switch s.behavior {
	case BehaviorFast:
		w.WriteHeader(http.StatusNoContent)
		s.successCount.Add(1)

	case BehaviorSlow:
		time.Sleep(s.slowDelay)
		w.WriteHeader(http.StatusNoContent)
		s.successCount.Add(1)

	case BehaviorHTTP500:
		w.WriteHeader(http.StatusInternalServerError)
		s.failureCount.Add(1)

	case BehaviorTimeout:
		// Sleep longer than any reasonable request timeout, but stop
		// early if the client disconnects. Without the context check
		// the handler occupies a goroutine for the full 30 seconds
		// even after the client has given up, which skews shutdown
		// timing and wastes resources at high concurrency.
		select {
		case <-time.After(30 * time.Second):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
			clientCanceled = true
		}

	case BehaviorTransient:
		seen := s.transientSeen.Add(1)
		if int(seen) <= s.transientFailures {
			w.WriteHeader(http.StatusServiceUnavailable)
			s.failureCount.Add(1)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		s.successCount.Add(1)

	case BehaviorOutage:
		// Time-based failure: return 503 until outageUntil, then 204.
		// Unlike transient-503, the trigger is wall-clock time, not
		// request count. This models a downstream that is genuinely
		// down for a fixed window rather than failing a fixed number
		// of requests.
		if time.Now().Before(s.outageUntil) {
			w.WriteHeader(http.StatusServiceUnavailable)
			s.failureCount.Add(1)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		s.successCount.Add(1)

	case BehaviorUnavailable:
		// Never reached — no listener is bound for this behavior.
		// This branch exists to keep the switch exhaustive.
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}
}

// URL returns the endpoint URL for seeding. For the unavailable behavior
// this is a URL that will refuse connections.
func (s *mockServer) URL() string {
	if s.Server == nil {
		return "http://127.0.0.1:1"
	}
	return s.Server.URL
}

// Close shuts down the mock server.
func (s *mockServer) Close() {
	if s.Server != nil {
		s.Server.Close()
	}
}

// OutageUntil returns the time at which the mock stops returning
// failures for BehaviorOutage. Zero for other behaviors.
func (s *mockServer) OutageUntil() time.Time {
	return s.outageUntil
}

// snapshot returns the current counters without holding the duration lock.
func (s *mockServer) snapshot() mockServerSnapshot {
	return mockServerSnapshot{
		Requests:      s.requestCount.Load(),
		Successes:     s.successCount.Load(),
		Failures:      s.failureCount.Load(),
		TransientHits: s.transientSeen.Load(),
	}
}

// durationSnapshot returns a copy of the recorded handler durations.
func (s *mockServer) durationSnapshot() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, len(s.durations))
	copy(out, s.durations)
	return out
}

// logServerStart prints a one-line startup message with the behavior and URL.
func logServerStart(logger *slog.Logger, s *mockServer) {
	logger.Info(
		"mock webhook server started",
		"behavior", s.behavior,
		"url", s.URL(),
		"outage_until", s.outageUntil.Format(time.RFC3339),
	)
}
