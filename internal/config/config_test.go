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

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.MongoDatabase != "relay" {
		t.Errorf("MongoDatabase = %q, want %q", cfg.MongoDatabase, "relay")
	}
	if cfg.WorkerPollSeconds != 2 {
		t.Errorf("WorkerPollSeconds = %d, want 2", cfg.WorkerPollSeconds)
	}
	if cfg.MaxDeliveryAttempts != 8 {
		t.Errorf("MaxDeliveryAttempts = %d, want 8", cfg.MaxDeliveryAttempts)
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
