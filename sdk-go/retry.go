package relay

import (
	"math"
	"net/http"
	"time"
)

// RetryConfig configures the automatic retry behavior.
type RetryConfig struct {
	MaxRetries int
	MinWait    time.Duration
	MaxWait    time.Duration
}

// DefaultRetryConfig is the default retry policy.
var DefaultRetryConfig = RetryConfig{
	MaxRetries: 3,
	MinWait:    500 * time.Millisecond,
	MaxWait:    5 * time.Second,
}

// isRetryableStatus returns true if the HTTP status code warrants a retry.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// backoff calculates the wait time for a given attempt (exponential with jitter).
func (r *RetryConfig) backoff(attempt int) time.Duration {
	// 2^attempt * MinWait, capped at MaxWait
	wait := time.Duration(math.Pow(2, float64(attempt))) * r.MinWait
	if wait > r.MaxWait {
		wait = r.MaxWait
	}
	return wait
}
