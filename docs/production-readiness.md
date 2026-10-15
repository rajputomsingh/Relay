# Production-Readiness Audit

Status as of commit `<sha>` on `main`.

This document records the outcome of a systematic review of Relay's
production-readiness posture. Every claim below is backed by a code
path, a test, or an explicit "no test exists" marker.

Legend:

- ✅ Verified by test or explicit code path.
- ⚠️ Code path exists but lacks a test.
- ❌ Confirmed gap.

---

## Authentication

| Check                                | Status | Evidence                                                                         |
| ------------------------------------ | ------ | -------------------------------------------------------------------------------- |
| Write endpoints require Bearer token | ✅     | `internal/handlers/events.go` — `create`, `replay`                               |
| Read endpoints require Bearer token  | ✅     | `internal/handlers/event_list.go`, `inspection.go`, `metrics.go`, `endpoints.go` |
| Constant-time key comparison         | ✅     | `subtle.ConstantTimeCompare` in every auth check                                 |
| Missing key rejected                 | ✅     | `TestEndpointsUnauthorized`                                                      |
| Wrong key rejected                   | ✅     | `TestEventListRejectsInvalidRequests/incorrect_API_key`                          |
| Empty configured key rejected        | ✅     | `TestMetricsHandlerRejectsEmptyConfiguredAPIKey`                                 |
| API key never logged                 | ✅     | Only `error_type` is logged in `cmd/relay/main.go`                               |
| Health endpoints public              | ✅     | `/healthz` and `/readyz` skip auth by design                                     |

---

## Request Validation

| Check                          | Status | Evidence                                                          |
| ------------------------------ | ------ | ----------------------------------------------------------------- |
| Body size limit                | ✅     | `http.MaxBytesReader(w, r.Body, maxEventBody)`                    |
| Unknown JSON fields rejected   | ✅     | `decoder.DisallowUnknownFields()`                                 |
| Single JSON object enforced    | ✅     | Second decode must return `io.EOF`                                |
| Required fields validated      | ✅     | `TestCreateEventRejectsInvalidRequests`                           |
| Idempotency-Key length bounded | ✅     | 1–128 characters                                                  |
| Endpoint URL validated         | ✅     | `internal/endpoints/endpoint.go` — absolute HTTP(S), no user info |
| Endpoint name bounded          | ✅     | 200 character limit                                               |

---

## Payload Limits

| Check                       | Status | Evidence                          |
| --------------------------- | ------ | --------------------------------- |
| Event body ≤ 1 MiB          | ✅     | `maxEventBody = 1 << 20`          |
| Attempt limit ≤ 100         | ✅     | `maxAttemptLimit = 100`           |
| Event list limit ≤ 100      | ✅     | `maxEventListLimit = 100`         |
| Response body drain bounded | ✅     | `io.LimitReader(resp.Body, 4096)` |

---

## Webhook Signing

| Check                         | Status | Evidence                                 |
| ----------------------------- | ------ | ---------------------------------------- |
| HMAC-SHA256 signing           | ✅     | `internal/security/signature.go`         |
| Constant-time verify          | ✅     | `hmac.Equal`                             |
| Tampered body rejected        | ✅     | `TestVerifyRejectsTamperedBody`          |
| Wrong secret rejected         | ✅     | `TestVerifyRejectsWrongSecret`           |
| Stale timestamp rejected      | ✅     | `examples/webhook_receiver.py`           |
| Secret never logged           | ✅     | No `slog` call includes `signing_secret` |
| Secret never in API responses | ✅     | `json:"-"` on the field                  |

---

## Graceful Shutdown

| Check                             | Status | Evidence                                                            |
| --------------------------------- | ------ | ------------------------------------------------------------------- |
| HTTP `Shutdown` on SIGTERM        | ✅     | `cmd/relay/main.go` — 10s timeout                                   |
| Worker waits for in-flight        | ✅     | `Worker.Run` uses `sync.WaitGroup`                                  |
| In-flight delivery retains lease  | ✅     | `TestWorkerIntegrationCancellationPreservesRecoverableDelivery`     |
| Worker stops on ctx cancel        | ✅     | `TestWorkerIntegrationRunStopsOnCancellation`                       |
| Whole-process shutdown under load | ⚠️     | Composition tested piecewise; no end-to-end SIGTERM-under-load test |

---

## MongoDB Outage

| Check                           | Status | Evidence                                                    |
| ------------------------------- | ------ | ----------------------------------------------------------- |
| Startup fails fast on empty URI | ✅     | `internal/database/mongo.go`                                |
| Startup ping has 5s timeout     | ✅     | `SetServerSelectionTimeout(5 * time.Second)`                |
| Startup ping cleans up client   | ✅     | `internal/database/mongo.go` — `client.Disconnect` on error |
| Handlers return 503 on error    | ✅     | `TestInspectionGetEventStorageFailure`                      |
| No internal error leakage       | ✅     | `TestInspectionIntegrationAttemptsDoNotLeakInternalErrors`  |
| Handler recovers after outage   | ✅     | `TestHandlerRecoversFromMongoOutage`                        |
| Worker survives outage          | ✅     | `TestWorkerRecoversFromMongoOutage`                         |
| Worker resumes after outage     | ✅     | `TestWorkerRecoversFromMongoOutage`                         |
| Delivery state preserved        | ✅     | `TestWorkerRecoversFromMongoOutage`                         |

---

## Health Endpoints

| Check                                 | Status | Evidence                                              |
| ------------------------------------- | ------ | ----------------------------------------------------- |
| `/healthz` returns 200                | ✅     | `TestLivenessReturnsOK`                               |
| `/healthz` rejects non-GET            | ✅     | `TestLivenessRejectsNonGET`                           |
| `/healthz` independent of Mongo       | ✅     | `TestLivenessDoesNotDependOnMongo`                    |
| `/readyz` returns 200 when Mongo up   | ✅     | `TestReadinessReturnsOKWhenMongoReachable`            |
| `/readyz` returns 503 when Mongo down | ✅     | `TestReadinessReturnsUnavailableWhenMongoUnreachable` |
| `/readyz` rejects non-GET             | ✅     | `TestReadinessRejectsNonGET`                          |
| No internal detail leaked             | ✅     | `TestHealthResponsesDoNotLeakInternalDetails`         |

---

## Confirmed Limitations

These are known gaps, not bugs. They are documented so operators can
plan around them.

### No per-endpoint rate limits

`RELAY_WORKER_CONCURRENCY` bounds total in-flight deliveries but not
per-endpoint request rate. A fast endpoint receives all available
capacity while a slow endpoint receives proportionally less. A future
milestone adds per-endpoint limits.

### No automatic replay of terminal failures

Deliveries that exhaust `max_attempts` reach `failed` and stay there.
Recovery requires an operator to issue `POST /v1/events/{id}/replay`
after the downstream recovers. No automatic retry beyond the configured
attempt count.

### No cross-region MongoDB failover

Relay uses a single MongoDB connection. If the primary is unreachable,
the driver fails over only within the configured replica set. Geo-
redundant deployments require MongoDB-level configuration.

### Whole-process shutdown under load is untested

Individual components are tested, but the composed behavior — SIGTERM
mid-delivery with many in-flight requests — is not covered by an
automated test. The design is expected to work (the HTTP server calls
`Shutdown`, the worker drains, in-flight deliveries retain leases), but
this path has not been exercised under load.

### No authentication for `/healthz` and `/readyz`

By design. Kubernetes probes and load balancers need unauthenticated
access. The responses contain only `service`, `status`, and `database`
fields — no sensitive data.

### No metrics for endpoint-specific error classes

`relay_deliveries_per_endpoint` distinguishes outcomes by status
(`delivered`, `failed`, etc.), but not by HTTP status class. A
downstream returning `500` and a downstream returning `503` look the
same. Detailed status codes are visible in `delivery_attempts` and
structured logs, not in metrics.

---

## Operational Procedures

### MongoDB outage

**Detection.**

- `/readyz` returns 503.
- `relay_worker_processing_errors_total` climbs.
- Handlers log `query failed` lines with generic error types.

**Behavior.**

- Handlers return 503 with `temporarily unavailable` messages. No data
  is lost; requests simply fail.
- The worker keeps polling. Each poll fails fast with a server-selection
  timeout.
- In-flight deliveries retain their leases. When MongoDB comes back,
  they are reclaimed on lease expiry.
- Once MongoDB is reachable, the next poll cycle succeeds. No process
  restart needed.

**Recovery.**

1. Restore MongoDB availability.
2. Confirm `/readyz` returns 200.
3. Confirm `relay_worker_processing_errors_total` stops incrementing.
4. Verify deliveries resume in `/v1/events/{id}/deliveries`.

### Worker stalled

**Detection.**

- `time() - relay_worker_last_processed_timestamp_seconds > 60`.
- `relay_deliveries_oldest_pending_seconds` grows.

**Behavior.**

- If the worker process is dead, the orchestrator restarts it. On
  restart, expired leases are reclaimed.
- If the worker is alive but stuck, the timestamp goes stale while the
  process continues.

**Recovery.**

1. Inspect logs for panics or repeated errors.
2. Verify MongoDB reachability.
3. Restart the process if the loop has deadlocked. No delivery state is
   lost — in-flight deliveries keep their leases and recover naturally.

### Delivery backlog growth

**Detection.**

- `relay_deliveries_oldest_pending_seconds` exceeds a threshold.
- `relay_worker_in_flight_deliveries` sits at the concurrency cap.

**Behavior.**

- Throughput is bounded by `RELAY_WORKER_CONCURRENCY / average_latency`.
- A slow endpoint can consume capacity that faster endpoints could use.

**Recovery.**

1. Identify the slow endpoint via `relay_deliveries_per_endpoint`.
2. If a single endpoint is failing, disable it via
   `DELETE /v1/endpoints/{id}` to unblock the queue.
3. Raise `RELAY_WORKER_CONCURRENCY` if the host can sustain it.
4. Add another Relay instance behind a load balancer.

### Manual replay after recovery

Terminal failures do not auto-recover. After a downstream service is
confirmed healthy:

```bash
curl -X POST "http://relay:8080/v1/events/${EVENT_ID}/replay" \
     -H "Authorization: Bearer ${RELAY_API_KEY}"
```

Replay resets the event's deliveries to `pending` with `attempts = 0`.
The worker picks them up on the next poll.

---

## Release Checklist

Before cutting a release:

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `go test -count=1 ./...` passes.
- [ ] MongoDB integration tests pass against a fresh container:
      `MONGODB_TEST_URI=mongodb://localhost:27017 go test -count=1 ./...`
- [ ] Outage integration tests pass:
      `MONGODB_TEST_CONTAINER=relay-mongo go test -count=1 -run Outage ./...`
- [ ] `git diff --check` reports no whitespace issues.
- [ ] `.env.example` reflects every environment variable read by
      `internal/config`.
- [ ] `README.md` describes any new configuration or behavior.
- [ ] `docs/observability.md` reflects the current metric set.
- [ ] Release notes describe behavioral changes.
