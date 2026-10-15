package delivery

import (
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// rateLimiter tracks per-endpoint request rates using token buckets.
// Buckets are created lazily and never removed — the number of
// endpoints is operator-controlled and small.
//
// Capacity equals the configured rate, allowing one-second bursts.
// Long-run throughput is bounded by the configured rate.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[bson.ObjectID]*tokenBucket
}

type tokenBucket struct {
	rate     float64
	capacity float64
	tokens   float64
	last     time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		buckets: make(map[bson.ObjectID]*tokenBucket),
	}
}

// allow reports whether one request to the given endpoint may proceed
// now. rps == 0 means unlimited.
func (r *rateLimiter) allow(
	endpointID bson.ObjectID,
	rps int,
) bool {
	if rps <= 0 {
		return true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	bucket, ok := r.buckets[endpointID]
	now := time.Now()

	if !ok {
		// First use: start with one token already consumed.
		r.buckets[endpointID] = &tokenBucket{
			rate:     float64(rps),
			capacity: float64(rps),
			tokens:   float64(rps) - 1,
			last:     now,
		}
		return true
	}

	elapsed := now.Sub(bucket.last).Seconds()
	bucket.tokens += elapsed * bucket.rate
	if bucket.tokens > bucket.capacity {
		bucket.tokens = bucket.capacity
	}
	bucket.last = now

	if bucket.tokens < 1 {
		return false
	}

	bucket.tokens--
	return true
}
