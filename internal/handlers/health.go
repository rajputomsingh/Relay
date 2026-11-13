package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// pinger is the subset of the MongoDB client used by readiness checks.
// Declared as an interface so tests can inject a fake without needing a
// live MongoDB instance.
type pinger interface {
	Ping(ctx context.Context, rp *readpref.ReadPref) error
}

// indexChecker reports whether the required database indexes exist.
// Optional: if nil, readiness does not check indexes.
type indexChecker interface {
	MissingIndexes(ctx context.Context) ([]string, error)
}

// workerPoller reports when the delivery worker last ran a poll cycle,
// whether or not that cycle found a delivery to process.
type workerPoller interface {
	LastPolledAt() time.Time
}

type HealthHandler struct {
	client       pinger
	indexChecker indexChecker

	worker       workerPoller
	pollInterval time.Duration
	version      string
	startedAt    time.Time
}

func NewHealthHandler(client pinger) *HealthHandler {
	return &HealthHandler{
		client:    client,
		version:   "dev",
		startedAt: time.Now(),
	}
}

// WithWorker attaches the delivery worker so /v1/health can report
// whether it is still polling. pollInterval is the worker's configured
// poll interval; the worker is stale after three missed intervals.
func (h *HealthHandler) WithWorker(worker workerPoller, pollInterval time.Duration) *HealthHandler {
	h.worker = worker
	h.pollInterval = pollInterval
	return h
}

// WithVersion sets the build version reported by /v1/health.
func (h *HealthHandler) WithVersion(version string) *HealthHandler {
	if version != "" {
		h.version = version
	}
	return h
}

// WithIndexChecker attaches an index checker to the readiness probe.
// When set, readiness returns 503 if any required index is missing.
func (h *HealthHandler) WithIndexChecker(c indexChecker) *HealthHandler {
	h.indexChecker = c
	return h
}

func (h *HealthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.liveness)
	mux.HandleFunc("/readyz", h.readiness)
	mux.HandleFunc("/v1/health", h.serviceHealth)
}

// liveness godoc
// @Summary      Liveness probe
// @Description  Returns 200 if the service is running.
// @Tags         health
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /healthz [get]
func (h *HealthHandler) liveness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"service": "relay",
		"status":  "ok",
	})
}

// readiness godoc
// @Summary      Readiness probe
// @Description  Returns 200 if the service is ready to accept traffic.
// @Description  Checks MongoDB connectivity and required index existence.
// @Tags         health
// @Produce      json
// @Success      200  {object}  map[string]string
// @Failure      503  {object}  map[string]string
// @Router       /readyz [get]
func (h *HealthHandler) readiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.client.Ping(ctx, readpref.Primary()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
		})
		return
	}

	if h.indexChecker != nil {
		missing, err := h.indexChecker.MissingIndexes(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
			})
			return
		}
		if len(missing) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ready",
		"database": "connected",
	})
}

type serviceHealthResponse struct {
	Status        string `json:"status"`
	Mongo         string `json:"mongo"`
	Worker        string `json:"worker,omitempty"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// serviceHealth godoc
// @Summary      Service health
// @Description  Reports the health of the Relay service itself: MongoDB
// @Description  reachability, delivery worker state, build version, and uptime.
// @Description  Returns 503 when MongoDB is unreachable; the worker is not
// @Description  checked in that case.
// @Tags         health
// @Produce      json
// @Success      200  {object}  serviceHealthResponse
// @Failure      503  {object}  serviceHealthResponse
// @Router       /v1/health [get]
func (h *HealthHandler) serviceHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	resp := serviceHealthResponse{
		Version:       h.version,
		UptimeSeconds: int64(time.Since(h.startedAt).Seconds()),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.client.Ping(ctx, readpref.Primary()); err != nil {
		resp.Status = "down"
		resp.Mongo = "unreachable"
		writeHealthJSON(w, http.StatusServiceUnavailable, resp)
		return
	}

	resp.Mongo = "ok"
	resp.Worker = h.workerStatus()
	if resp.Worker == "ok" {
		resp.Status = "ok"
	} else {
		resp.Status = "degraded"
	}
	writeHealthJSON(w, http.StatusOK, resp)
}

// workerStatus is "disabled" when no worker is attached or it has never
// polled, "stale" when its last poll is older than three poll intervals,
// and "ok" otherwise.
func (h *HealthHandler) workerStatus() string {
	if h.worker == nil {
		return "disabled"
	}
	last := h.worker.LastPolledAt()
	if last.IsZero() {
		return "disabled"
	}
	if time.Since(last) > 3*h.pollInterval {
		return "stale"
	}
	return "ok"
}

func writeHealthJSON(w http.ResponseWriter, status int, body serviceHealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
