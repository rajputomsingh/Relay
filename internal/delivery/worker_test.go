package delivery

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	worker := NewWorker(nil, nil, Config{
		MaxAttempts:    8,
		RequestTimeout: 10 * time.Second,
		LeaseDuration:  30 * time.Second,
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
	worker := NewWorker(nil, nil, Config{
		WebhookURL:     "https://example.com/webhook",
		RequestTimeout: 10 * time.Second,
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
		WebhookURL:     "https://example.com/webhook",
		PollInterval:   2 * time.Second,
		MaxAttempts:    8,
		RequestTimeout: 10 * time.Second,
		LeaseDuration:  30 * time.Second,
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
			name: "zero max attempts",
			modify: func(c *Config) {
				c.MaxAttempts = 0
			},
			wantErr: true,
		},
		{
			name: "negative max attempts",
			modify: func(c *Config) {
				c.MaxAttempts = -1
			},
			wantErr: true,
		},
		{
			name: "missing webhook URL",
			modify: func(c *Config) {
				c.WebhookURL = ""
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.modify(&cfg)

			worker := NewWorker(nil, nil, cfg)
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

func TestWorkerSignerRejectsShortSecret(t *testing.T) {
	worker := NewWorker(nil, nil, Config{
		WebhookURL:           "https://example.com/hook",
		PollInterval:         2 * time.Second,
		MaxAttempts:          3,
		RequestTimeout:       10 * time.Second,
		LeaseDuration:        30 * time.Second,
		WebhookSigningSecret: "short",
	})

	if err := worker.validateConfig(); err == nil {
		t.Fatal("expected validation error for short signing secret")
	}
}

func TestWorkerSignerConfigured(t *testing.T) {
	worker := NewWorker(nil, nil, Config{
		WebhookURL:           "https://example.com/hook",
		PollInterval:         2 * time.Second,
		MaxAttempts:          3,
		RequestTimeout:       10 * time.Second,
		LeaseDuration:        30 * time.Second,
		WebhookSigningSecret: "0123456789abcdef0123456789abcdef",
	})

	if err := worker.validateConfig(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if worker.signer == nil {
		t.Fatal("expected signer to be configured")
	}
}
