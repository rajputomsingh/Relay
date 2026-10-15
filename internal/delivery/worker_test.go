package delivery

import (
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRetryDelay(t *testing.T) {
	worker := NewWorker(nil, nil, nil, nil, Config{
		PollInterval:   2 * time.Second,
		RequestTimeout: 10 * time.Second,
		LeaseDuration:  30 * time.Second,
		Concurrency:    1,
	})

	tests := []struct {
		name    string
		attempt int
		min     time.Duration
		max     time.Duration
	}{
		{"first attempt", 1, 2 * time.Second, 2500 * time.Millisecond},
		{"second attempt", 2, 4 * time.Second, 5 * time.Second},
		{"third attempt", 3, 8 * time.Second, 10 * time.Second},
		{"capped backoff", 10, 5 * time.Minute, 6*time.Minute + 15*time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := worker.retryDelay(tt.attempt)

			if delay < tt.min || delay > tt.max {
				t.Fatalf(
					"retryDelay(%d) = %v; want between %v and %v",
					tt.attempt,
					delay,
					tt.min,
					tt.max,
				)
			}
		})
	}
}

func TestWorkerDoesNotFollowRedirects(t *testing.T) {
	worker := NewWorker(nil, nil, nil, nil, Config{
		PollInterval:   2 * time.Second,
		RequestTimeout: 10 * time.Second,
		LeaseDuration:  30 * time.Second,
		Concurrency:    1,
	})

	req, err := http.NewRequest(
		http.MethodGet,
		"https://example.com/redirect",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = worker.client.CheckRedirect(req, nil)
	if err != http.ErrUseLastResponse {
		t.Fatalf("CheckRedirect() = %v; want %v", err, http.ErrUseLastResponse)
	}
}

func TestValidateConfig(t *testing.T) {
	valid := Config{
		PollInterval:   2 * time.Second,
		RequestTimeout: 10 * time.Second,
		LeaseDuration:  30 * time.Second,
		Concurrency:    1,
	}
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:   "valid configuration",
			modify: func(*Config) {},
		},
		{
			name: "zero poll interval",
			modify: func(c *Config) {
				c.PollInterval = 0
			},
			wantErr: true,
		},
		{
			name: "negative poll interval",
			modify: func(c *Config) {
				c.PollInterval = -time.Second
			},
			wantErr: true,
		},
		{
			name: "zero request timeout",
			modify: func(c *Config) {
				c.RequestTimeout = 0
			},
			wantErr: true,
		},
		{
			name: "negative request timeout",
			modify: func(c *Config) {
				c.RequestTimeout = -time.Second
			},
			wantErr: true,
		},
		{
			name: "zero lease duration",
			modify: func(c *Config) {
				c.LeaseDuration = 0
			},
			wantErr: true,
		},
		{
			name: "lease shorter than request timeout",
			modify: func(c *Config) {
				c.LeaseDuration = 9 * time.Second
			},
			wantErr: true,
		},
		{
			name: "lease equals request timeout",
			modify: func(c *Config) {
				c.LeaseDuration = c.RequestTimeout
			},
			wantErr: true,
		},
		{
			name: "zero concurrency",
			modify: func(c *Config) {
				c.Concurrency = 0
			},
			wantErr: true,
		},
		{
			name: "negative concurrency",
			modify: func(c *Config) {
				c.Concurrency = -1
			},
			wantErr: true,
		},
		{
			name: "concurrency above maximum",
			modify: func(c *Config) {
				c.Concurrency = 33
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.modify(&cfg)

			worker := NewWorker(nil, nil, nil, nil, cfg)
			err := worker.validateConfig()

			if (err != nil) != tt.wantErr {
				t.Fatalf(
					"validateConfig() error = %v, wantErr %v",
					err,
					tt.wantErr,
				)
			}
		})
	}
}

// ---------------------------------------------------------------
// Rate limiter
// ---------------------------------------------------------------

func TestRateLimiterUnlimited(t *testing.T) {
	rl := newRateLimiter()
	id := bson.NewObjectID()

	for i := 0; i < 1000; i++ {
		if !rl.allow(id, 0) {
			t.Fatalf("unlimited limiter denied request %d", i)
		}
	}
}

func TestRateLimiterRespectsRate(t *testing.T) {
	rl := newRateLimiter()
	id := bson.NewObjectID()

	const rps = 5

	for i := 0; i < rps; i++ {
		if !rl.allow(id, rps) {
			t.Fatalf("request %d within capacity was denied", i)
		}
	}

	if rl.allow(id, rps) {
		t.Fatal("request beyond capacity was allowed")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	rl := newRateLimiter()
	id := bson.NewObjectID()

	const rps = 10

	for i := 0; i < rps; i++ {
		rl.allow(id, rps)
	}
	if rl.allow(id, rps) {
		t.Fatal("bucket should be empty")
	}

	time.Sleep(250 * time.Millisecond)

	if !rl.allow(id, rps) {
		t.Fatal("bucket should have refilled at least one token")
	}
}

func TestRateLimiterIndependentPerEndpoint(t *testing.T) {
	rl := newRateLimiter()
	a := bson.NewObjectID()
	b := bson.NewObjectID()

	for i := 0; i < 3; i++ {
		rl.allow(a, 3)
	}
	if rl.allow(a, 3) {
		t.Fatal("A should be exhausted")
	}

	if !rl.allow(b, 3) {
		t.Fatal("B should have full capacity")
	}
}
