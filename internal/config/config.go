package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Environment          string
	Port                 string
	MongoURI             string
	MongoDatabase        string
	RelayAPIKey          string
	WebhookURL           string
	WorkerPollSeconds    int
	MaxDeliveryAttempts  int
	WebhookSigningSecret string
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("failed to load configuration")
	}

	cfg := Config{
		Environment:   getEnv("APP_ENV", "development"),
		Port:          getEnv("PORT", "8080"),
		MongoURI:      strings.TrimSpace(os.Getenv("MONGODB_URI")),
		MongoDatabase: getEnv("MONGODB_DATABASE", "relay"),
		RelayAPIKey:   strings.TrimSpace(os.Getenv("RELAY_API_KEY")),
		WebhookURL:    strings.TrimSpace(os.Getenv("RELAY_WEBHOOK_URL")),
	}

	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("PORT must be between 1 and 65535")
	}

	if cfg.MongoURI == "" {
		return Config{}, errors.New("MONGODB_URI is required")
	}
	if cfg.MongoDatabase == "" {
		return Config{}, errors.New("MONGODB_DATABASE is required")
	}
	if len(cfg.RelayAPIKey) < 32 {
		return Config{}, errors.New("RELAY_API_KEY must contain at least 32 characters")
	}

	if cfg.Environment != "development" &&
		cfg.Environment != "test" &&
		cfg.Environment != "production" {
		return Config{}, errors.New("invalid APP_ENV value")
	}

	// The destination is operator-controlled configuration.
	if cfg.WebhookURL != "" {
		u, err := url.ParseRequestURI(cfg.WebhookURL)
		if err != nil || u.Hostname() == "" ||
			(u.Scheme != "https" && u.Scheme != "http") ||
			u.User != nil {
			return Config{}, errors.New(
				"RELAY_WEBHOOK_URL must be an absolute HTTP(S) URL without user information",
			)
		}

		if cfg.Environment == "production" && u.Scheme != "https" {
			return Config{}, errors.New(
				"RELAY_WEBHOOK_URL must use HTTPS in production",
			)
		}
	}

	cfg.WorkerPollSeconds, err = strconv.Atoi(
		getEnv("RELAY_WORKER_POLL_SECONDS", "2"),
	)
	if err != nil || cfg.WorkerPollSeconds < 1 || cfg.WorkerPollSeconds > 60 {
		return Config{}, errors.New(
			"RELAY_WORKER_POLL_SECONDS must be between 1 and 60",
		)
	}

	cfg.MaxDeliveryAttempts, err = strconv.Atoi(
		getEnv("RELAY_MAX_DELIVERY_ATTEMPTS", "8"),
	)
	if err != nil || cfg.MaxDeliveryAttempts < 1 || cfg.MaxDeliveryAttempts > 20 {
		return Config{}, errors.New(
			"RELAY_MAX_DELIVERY_ATTEMPTS must be between 1 and 20",
		)
	}

	cfg.WebhookSigningSecret = strings.TrimSpace(
		os.Getenv("RELAY_WEBHOOK_SIGNING_SECRET"),
	)

	if cfg.WebhookSigningSecret != "" {
		if len(cfg.WebhookSigningSecret) < 32 {
			return Config{}, errors.New(
				"RELAY_WEBHOOK_SIGNING_SECRET must contain at least 32 characters",
			)
		}
		if cfg.WebhookURL == "" {
			return Config{}, errors.New(
				"RELAY_WEBHOOK_SIGNING_SECRET requires RELAY_WEBHOOK_URL",
			)
		}
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}