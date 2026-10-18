package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// requireDocker skips the test if the docker CLI is unavailable.
func requireDocker(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
}

// requireContainer skips the test if the named container does not exist.
func requireContainer(t *testing.T, name string) {
	t.Helper()

	cmd := exec.Command(
		"docker", "container", "inspect", name,
		"--format", "{{.State.Status}}",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("docker container %q not found: %v", name, err)
	}

	status := strings.TrimSpace(string(out))
	if status != "running" {
		t.Skipf(
			"docker container %q is not running (status %q)",
			name,
			status,
		)
	}
}

func dockerStop(t *testing.T, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stop", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf(
			"docker stop %s failed: %v; output=%s",
			name,
			err,
			string(out),
		)
	}
}

func dockerStart(t *testing.T, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "start", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf(
			"docker start %s failed: %v; output=%s",
			name,
			err,
			string(out),
		)
	}
}

// waitForMongoPing blocks until a Ping succeeds or the deadline elapses.
func waitForMongoPing(
	t *testing.T,
	client *mongo.Client,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(
			context.Background(), 2*time.Second,
		)
		err := client.Ping(ctx, readpref.Primary())
		cancel()

		if err == nil {
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf(
		"MongoDB did not become reachable within %s: %v",
		timeout,
		lastErr,
	)
}

// waitForMongoUnreachable blocks until a Ping fails or the deadline elapses.
func waitForMongoUnreachable(
	t *testing.T,
	client *mongo.Client,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	lastErr := error(nil)

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(
			context.Background(), 2*time.Second,
		)
		err := client.Ping(ctx, readpref.Primary())
		cancel()

		if err != nil {
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf(
		"MongoDB remained reachable after %s; expected outage. "+
			"Make sure MONGODB_TEST_URI points at the container named "+
			"by MONGODB_TEST_CONTAINER (%q), not at a different MongoDB "+
			"instance. last error: %v",
		timeout,
		os.Getenv("MONGODB_TEST_CONTAINER"),
		lastErr,
	)
}

func TestHandlerRecoversFromMongoOutage(t *testing.T) {
	requireDocker(t)

	uri := strings.TrimSpace(os.Getenv("MONGODB_TEST_URI"))
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run MongoDB outage tests")
	}

	// The outage test requires direct control over the container that
	// serves MONGODB_TEST_URI. This is only possible when the operator
	// explicitly names the container via MONGODB_TEST_CONTAINER. In
	// CI, MongoDB runs as a CircleCI-managed service container that
	// the test process cannot stop, so this variable is unset and the
	// test skips cleanly.
	container := strings.TrimSpace(
		os.Getenv("MONGODB_TEST_CONTAINER"),
	)
	if container == "" {
		t.Skip(
			"MONGODB_TEST_CONTAINER not set; skipping outage recovery " +
				"test (requires the ability to stop the container " +
				"serving MONGODB_TEST_URI)",
		)
	}
	requireContainer(t, container)

	// Establish the MongoDB client against the currently-running container.
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetAppName("relay-outage-integration-tests").
		SetServerSelectionTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("connect to test MongoDB: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()
		_ = client.Disconnect(ctx)
	}()

	db := client.Database("relay_outage_test")
	collection := db.Collection("events")

	t.Cleanup(func() {
		// Best-effort cleanup. If MongoDB is still down, that's fine.
		ctx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()
		_ = db.Drop(ctx)
	})

	// Handlers under test.
	listHandler := NewEventsListHandler(
		collection, "outage-integration-test-key",
	)
	mux := http.NewServeMux()
	listHandler.Register(mux)

	serve := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodGet, "/v1/events", nil,
		)
		req.Header.Set(
			"Authorization",
			"Bearer outage-integration-test-key",
		)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	// -------------------------------------------------------------
	// Step 1 — normal operation while MongoDB is running.
	// -------------------------------------------------------------

	waitForMongoPing(t, client, 10*time.Second)

	healthy := serve()
	if healthy.Code != http.StatusOK {
		t.Fatalf(
			"healthy request: status = %d, want 200; body=%s",
			healthy.Code,
			healthy.Body.String(),
		)
	}

	// -------------------------------------------------------------
	// Step 2 — stop MongoDB, verify handler returns 503 with no
	// internal detail leaked.
	// -------------------------------------------------------------

	dockerStop(t, container)
	defer dockerStart(t, container)

	waitForMongoUnreachable(t, client, 15*time.Second)

	outage := serve()
	if outage.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"outage request: status = %d, want 503; body=%s",
			outage.Code,
			outage.Body.String(),
		)
	}

	body := outage.Body.String()
	if !strings.Contains(body, "temporarily unavailable") {
		t.Errorf(
			"outage response lacks generic message: %s",
			body,
		)
	}

	// The response must not echo the underlying driver error, which
	// can contain hostnames, ports, or connection strings.
	for _, forbidden := range []string{
		"server selection",
		"connection refused",
		"mongodb://",
		"localhost",
		"context deadline",
	} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf(
				"outage response leaks internal detail %q: %s",
				forbidden,
				body,
			)
		}
	}

	// -------------------------------------------------------------
	// Step 3 — restart MongoDB, verify handler recovers without
	// restarting the process.
	// -------------------------------------------------------------

	dockerStart(t, container)
	waitForMongoPing(t, client, 30*time.Second)

	// Give the handler's connection pool a moment to re-establish.
	// The driver reconnects lazily on the next request.
	recovered := serve()
	if recovered.Code != http.StatusOK {
		t.Fatalf(
			"recovery request: status = %d, want 200; body=%s",
			recovered.Code,
			recovered.Body.String(),
		)
	}
}
