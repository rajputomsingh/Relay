package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configEnvKeys lists every environment variable read by Load. Keep in
// sync with config.go.
var configEnvKeys = []string{
	"APP_ENV",
	"PORT",
	"MONGODB_URI",
	"MONGODB_DATABASE",
	"RELAY_API_KEY",
	"RELAY_WEBHOOK_URL",
	"RELAY_WEBHOOK_URL_ALLOW_PRIVATE",
	"RELAY_WORKER_POLL_SECONDS",
	"RELAY_WORKER_CONCURRENCY",
	"RELAY_MAX_DELIVERY_ATTEMPTS",
	"RELAY_WEBHOOK_SIGNING_SECRET",
}

// isolateEnv saves the process environment and restores it after the test.
// godotenv.Load writes values through os.Setenv directly, bypassing
// t.Setenv, so tests that exercise .env loading must snapshot the whole
// environment themselves.
func isolateEnv(t *testing.T) {
	t.Helper()

	before := os.Environ()
	t.Cleanup(func() {
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			_ = os.Unsetenv(key)
		}
		for _, entry := range before {
			key, value, _ := strings.Cut(entry, "=")
			_ = os.Setenv(key, value)
		}
	})
}

// clearConfigEnv removes every config key from the process environment so
// values can only come from the .env file under test.
func clearConfigEnv(t *testing.T) {
	t.Helper()

	for _, key := range configEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
		}
	}
}

// chdirWithEnvFile switches into a temporary directory containing a .env
// file with the given contents. The directory is removed with the test.
func chdirWithEnvFile(t *testing.T, contents string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, ".env"), []byte(contents), 0o600,
	); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Chdir(dir)
}

func TestLoadReadsEnvFileFromWorkingDirectory(t *testing.T) {
	isolateEnv(t)
	clearConfigEnv(t)
	chdirWithEnvFile(t, strings.Join([]string{
		"APP_ENV=development",
		"PORT=8080",
		"MONGODB_URI=mongodb://localhost:27017",
		"MONGODB_DATABASE=relay",
		"RELAY_API_KEY=0123456789abcdef0123456789abcdef",
		"",
	}, "\n"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.MongoURI != "mongodb://localhost:27017" {
		t.Errorf("MongoURI = %q, want value from .env", cfg.MongoURI)
	}
	if cfg.RelayAPIKey != "0123456789abcdef0123456789abcdef" {
		t.Errorf("RelayAPIKey = %q, want value from .env", cfg.RelayAPIKey)
	}
	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, want development", cfg.Environment)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
}

func TestLoadProcessEnvironmentWinsOverEnvFile(t *testing.T) {
	isolateEnv(t)
	clearConfigEnv(t)
	chdirWithEnvFile(t, strings.Join([]string{
		"APP_ENV=development",
		"PORT=9999",
		"MONGODB_URI=mongodb://from-file:27017",
		"MONGODB_DATABASE=from-file",
		"RELAY_API_KEY=0123456789abcdef0123456789abcdef",
		"",
	}, "\n"))

	// Explicit process environment must take precedence over .env.
	t.Setenv("MONGODB_URI", "mongodb://from-process:27017")
	t.Setenv("PORT", "8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.MongoURI != "mongodb://from-process:27017" {
		t.Errorf(
			"MongoURI = %q, want process environment value to win over .env",
			cfg.MongoURI,
		)
	}
	if cfg.Port != "8080" {
		t.Errorf(
			"Port = %q, want process environment value to win over .env",
			cfg.Port,
		)
	}
	// Variables not set in the process still come from the file.
	if cfg.MongoDatabase != "from-file" {
		t.Errorf("MongoDatabase = %q, want value from .env", cfg.MongoDatabase)
	}
}

func TestLoadRejectsMalformedEnvFile(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{
			name:     "line without assignment",
			contents: "this is not an assignment\n",
		},
		{
			name:     "invalid character in key",
			contents: "MONGODB$URI=mongodb://localhost:27017\n",
		},
		{
			name:     "unterminated quoted value",
			contents: "MONGODB_URI=\"mongodb://localhost:27017\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateEnv(t)
			clearConfigEnv(t)
			chdirWithEnvFile(t, tt.contents)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() expected an error for malformed .env, got nil")
			}
			if !strings.Contains(err.Error(), ".env") {
				t.Errorf(
					"error %q should name the .env file it failed to parse",
					err,
				)
			}
		})
	}
}

// TestEnvExampleListsEveryConfigVariable guards acceptance criterion:
// .env.example must list every configuration variable used by Load.
func TestEnvExampleListsEveryConfigVariable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	contents := string(raw)

	for _, key := range configEnvKeys {
		if !strings.Contains(contents, key+"=") {
			t.Errorf(".env.example is missing %s=", key)
		}
	}
}

func TestLoadSucceedsWithoutEnvFile(t *testing.T) {
	isolateEnv(t)
	clearConfigEnv(t)
	// Empty working directory: no .env file at all.
	t.Chdir(t.TempDir())

	t.Setenv("APP_ENV", "development")
	t.Setenv("PORT", "8080")
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "relay")
	t.Setenv("RELAY_API_KEY", "0123456789abcdef0123456789abcdef")

	cfg, err := Load()
	if err != nil {
		t.Fatalf(
			"Load() should succeed when no .env file exists, got: %v", err,
		)
	}
	if cfg.MongoURI != "mongodb://localhost:27017" {
		t.Errorf("MongoURI = %q, want process environment value", cfg.MongoURI)
	}
}
