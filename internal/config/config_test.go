package config

import "testing"

func setValidEnvironment(t *testing.T) {
	t.Helper()

	values := map[string]string{
		"APP_ENV":                     "development",
		"PORT":                        "8080",
		"MONGODB_URI":                 "mongodb://localhost:27017",
		"MONGODB_DATABASE":            "relay",
		"RELAY_API_KEY":               "0123456789abcdef0123456789abcdef",
		"RELAY_WEBHOOK_URL":           "",
		"RELAY_WORKER_POLL_SECONDS":   "2",
		"RELAY_MAX_DELIVERY_ATTEMPTS": "8",
	}

	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestLoadValidConfiguration(t *testing.T) {
	setValidEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, want development", cfg.Environment)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.MongoDatabase != "relay" {
		t.Errorf("MongoDatabase = %q, want relay", cfg.MongoDatabase)
	}
	if cfg.WorkerPollSeconds != 2 {
		t.Errorf("WorkerPollSeconds = %d, want 2", cfg.WorkerPollSeconds)
	}
	if cfg.MaxDeliveryAttempts != 8 {
		t.Errorf("MaxDeliveryAttempts = %d, want 8", cfg.MaxDeliveryAttempts)
	}
}

func TestLoadDefaultsEmptyDatabaseName(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("MONGODB_DATABASE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.MongoDatabase != "relay" {
		t.Errorf("MongoDatabase = %q, want relay", cfg.MongoDatabase)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func()
	}{
		{
			name: "missing MongoDB URI",
			change: func() {
				t.Setenv("MONGODB_URI", "")
			},
		},
		{
			name: "invalid port text",
			change: func() {
				t.Setenv("PORT", "not-a-port")
			},
		},
		{
			name: "port below minimum",
			change: func() {
				t.Setenv("PORT", "0")
			},
		},
		{
			name: "port above maximum",
			change: func() {
				t.Setenv("PORT", "65536")
			},
		},
		{
			name: "API key too short",
			change: func() {
				t.Setenv("RELAY_API_KEY", "short")
			},
		},
		{
			name: "invalid environment",
			change: func() {
				t.Setenv("APP_ENV", "staging")
			},
		},
		{
			name: "malformed webhook URL",
			change: func() {
				t.Setenv("RELAY_WEBHOOK_URL", "not-a-url")
			},
		},
		{
			name: "unsupported webhook scheme",
			change: func() {
				t.Setenv("RELAY_WEBHOOK_URL", "ftp://example.com/hook")
			},
		},
		{
			name: "webhook URL contains credentials",
			change: func() {
				t.Setenv("RELAY_WEBHOOK_URL", "https://user:pass@example.com/hook")
			},
		},
		{
			name: "production webhook requires HTTPS",
			change: func() {
				t.Setenv("APP_ENV", "production")
				t.Setenv("RELAY_WEBHOOK_URL", "http://example.com/hook")
			},
		},
		{
			name: "poll interval below minimum",
			change: func() {
				t.Setenv("RELAY_WORKER_POLL_SECONDS", "0")
			},
		},
		{
			name: "poll interval above maximum",
			change: func() {
				t.Setenv("RELAY_WORKER_POLL_SECONDS", "61")
			},
		},
		{
			name: "invalid poll interval text",
			change: func() {
				t.Setenv("RELAY_WORKER_POLL_SECONDS", "invalid")
			},
		},
		{
			name: "delivery attempts below minimum",
			change: func() {
				t.Setenv("RELAY_MAX_DELIVERY_ATTEMPTS", "0")
			},
		},
		{
			name: "delivery attempts above maximum",
			change: func() {
				t.Setenv("RELAY_MAX_DELIVERY_ATTEMPTS", "21")
			},
		},
		{
			name: "invalid delivery attempts text",
			change: func() {
				t.Setenv("RELAY_MAX_DELIVERY_ATTEMPTS", "invalid")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidEnvironment(t)
			tt.change()

			if _, err := Load(); err == nil {
				t.Fatal("Load() expected an error, got nil")
			}
		})
	}
}

func TestLoadAcceptsHTTPSWebhookInProduction(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("RELAY_WEBHOOK_URL", "https://example.com/webhook")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() rejected valid production configuration: %v", err)
	}
}
