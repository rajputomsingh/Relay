# Observability

Relay exposes three operational surfaces: structured logs, a
Prometheus-compatible metrics endpoint, and read-only inspection APIs.
This document describes what each provides, how to interpret it, and
how to alert on it.

---

## Metrics

`GET /metrics` returns Prometheus text format (`text/plain; version=0.0.4`).

Authentication uses the same `Authorization: Bearer <RELAY_API_KEY>`
header as other operator endpoints. The response is marked
`Cache-Control: no-store`.

The handler performs a single read-consistent snapshot per request. If
MongoDB is unreachable, it returns `503 Service Unavailable` with a
generic message — internal errors are not exposed to the scraper.

### Delivery state

| Metric                                    | Type  | Labels                  | Meaning                                                                                              |
| ----------------------------------------- | ----- | ----------------------- | ---------------------------------------------------------------------------------------------------- |
| `relay_deliveries`                        | gauge | `status`                | Number of delivery records in each status. Statuses: `pending`, `processing`, `delivered`, `failed`. |
| `relay_deliveries_per_endpoint`           | gauge | `endpoint_id`, `status` | Number of delivery records grouped by endpoint and status.                                           |
| `relay_deliveries_oldest_pending_seconds` | gauge | —                       | Age in seconds of the oldest delivery in `pending` state. Absent when no pending deliveries exist.   |
| `relay_deliveries_terminal_failures`      | gauge | —                       | Total deliveries that reached the `failed` state.                                                    |

### Attempt history

| Metric                            | Type      | Meaning                                                                  |
| --------------------------------- | --------- | ------------------------------------------------------------------------ |
| `relay_delivery_attempts`         | gauge     | Total persisted attempt records.                                         |
| `relay_delivery_retries`          | gauge     | Attempts with `attempt_number > 1`.                                      |
| `relay_delivery_duration_seconds` | histogram | Attempt duration in seconds. Buckets: 100ms, 500ms, 1s, 5s, 10s, `+Inf`. |

The histogram has companion series `_sum` (total seconds) and `_count`
(total attempts). Use `_sum / _count` for the average.

### Worker health

| Metric                                          | Type  | Meaning                                                                                                               |
| ----------------------------------------------- | ----- | --------------------------------------------------------------------------------------------------------------------- |
| `relay_worker_last_processed_timestamp_seconds` | gauge | Unix timestamp of the most recent successful processing cycle. Absent until the worker completes its first cycle.     |
| `relay_worker_processing_errors_total`          | gauge | Count of non-transient processing errors since process start. Does not include the normal "no due deliveries" signal. |

### Legacy event metrics

Retained for backward compatibility with operators tracking the
pre-multi-endpoint schema.

| Metric         | Type  | Labels   | Meaning                                                                                          |
| -------------- | ----- | -------- | ------------------------------------------------------------------------------------------------ |
| `relay_events` | gauge | `status` | Event-level counts by legacy `delivery_status`. Not authoritative after multi-endpoint delivery. |

### Cardinality

Metric labels are bounded by design:

- `endpoint_id` — cardinality equals the number of registered
  endpoints, which is operator-controlled and typically small
  (tens, not thousands).
- `status` — four values for deliveries, five for the legacy event
  metric.

No event IDs, idempotency keys, URLs, source names, or free-form
strings appear as labels. Per-delivery detail lives in structured logs
and the inspection APIs, not in metrics.

---

## Alert Recipes

### Worker stalled

No successful processing cycle in 60 seconds.

```promql
time() - relay_worker_last_processed_timestamp_seconds > 60
```

This is the primary liveness check for the delivery pipeline. A stalled
worker holds the backlog silently; without this alert, the first visible
signal is a spike in `relay_deliveries{status="pending"}`.

### Backlog growing

Oldest pending delivery is more than 5 minutes old.

```promql
relay_deliveries_oldest_pending_seconds > 300
```

Indicates the worker is running but not keeping up, or a subset of
endpoints is unreachable and blocking the queue.

### Terminal failure accumulation

Deliveries transitioning to `failed` within the last 15 minutes.

```promql
increase(relay_deliveries_terminal_failures[15m]) > 0
```

Any nonzero value deserves investigation. A small number is normal
under transient downstream outages; a sustained rate indicates a
persistent problem.

### Endpoint-specific failures

One endpoint is failing while others succeed. Used to distinguish
endpoint-specific issues from global ones.

```promql
relay_deliveries_per_endpoint{status="failed"} > 0
  unless on(endpoint_id)
relay_deliveries_per_endpoint{status="delivered"} > 0
```

### Slow downstream

Attempt duration p95 above 5 seconds.

```promql
histogram_quantile(
  0.95,
  rate(relay_delivery_duration_seconds_bucket[5m])
) > 5
```

### Retry storm

Retries growing faster than successful first attempts.

```promql
rate(relay_delivery_retries[5m]) > 0.1
```

Adjust the threshold to match expected traffic.

---

## Logs

Relay emits structured JSON logs via `slog`. Each delivery attempt
produces exactly one completion log line, at `INFO` on success and
`WARN` on failure.

### Delivery attempt fields

| Field         | Meaning                                                                            |
| ------------- | ---------------------------------------------------------------------------------- |
| `delivery_id` | Delivery document `_id`. Correlates the log line with the `deliveries` collection. |
| `event_id`    | Event `_id`.                                                                       |
| `endpoint_id` | Endpoint `_id`.                                                                    |
| `attempt`     | Attempt number for this delivery (1-based).                                        |
| `outcome`     | `delivered` or `failed`.                                                           |
| `duration_ms` | HTTP request duration in milliseconds.                                             |
| `http_status` | Response status code. `0` for transport errors.                                    |
| `error_type`  | Go error type when `outcome = failed`. Never contains payloads or secrets.         |

### Example

Success:

```json
{
  "time": "2026-10-15T10:22:41.123Z",
  "level": "INFO",
  "msg": "webhook delivery attempt completed",
  "delivery_id": "6acf1234...",
  "event_id": "6acf0001...",
  "endpoint_id": "6acf0002...",
  "attempt": 1,
  "outcome": "delivered",
  "duration_ms": 8,
  "http_status": 200
}
```

Retryable failure:

```json
{
  "time": "2026-10-15T10:22:41.456Z",
  "level": "WARN",
  "msg": "webhook delivery attempt completed",
  "delivery_id": "6acf1234...",
  "event_id": "6acf0001...",
  "endpoint_id": "6acf0002...",
  "attempt": 2,
  "outcome": "failed",
  "duration_ms": 34,
  "http_status": 503,
  "error_type": "*errors.errorString"
}
```

### Correlating across surfaces

Given a `delivery_id`:

```bash
journalctl -u relay | grep <delivery_id>
```

```javascript
// mongosh
db.deliveries.findOne({ _id: ObjectId("<delivery_id>") })
db.delivery_attempts.find({ event_id: <event_id>, endpoint_id: <endpoint_id> })
```

The API also exposes the same data:

```
GET /v1/events/{id}/deliveries
GET /v1/events/{id}/attempts
```

### What is never logged

- Signing secrets (`signing_secret`, `RELAY_WEBHOOK_SIGNING_SECRET`).
- API keys (`RELAY_API_KEY`, `Authorization` headers).
- Full webhook payload bodies. Only IDs and metadata are logged.
- Raw downstream error bodies. Only the Go error type is recorded.

The `last_error` field on deliveries (and the attempt `error` field)
may contain short diagnostic strings such as `webhook returned HTTP 503`
or `dial tcp: connection refused`. These never include credentials
because the request URL is not echoed back in error strings produced by
Relay.

---

## Health Endpoints

| Endpoint   | Method | Purpose                          | Status codes                                                                 |
| ---------- | ------ | -------------------------------- | ---------------------------------------------------------------------------- |
| `/healthz` | GET    | Liveness. Process is running.    | `200` normally; `405` on other methods                                       |
| `/readyz`  | GET    | Readiness. MongoDB is reachable. | `200` when ready; `503` when the database ping fails; `405` on other methods |

### Liveness vs readiness

`/healthz` is intentionally independent of MongoDB. A database outage
should not cause the orchestrator to kill and restart the process —
that would make recovery harder, not easier.

`/readyz` pings MongoDB with a 3-second timeout on every request. When
it fails, the process is running but cannot serve traffic. Load
balancers should drain connections in this state; the process itself
should not be restarted.

Kubernetes example:

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  periodSeconds: 30
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  periodSeconds: 10
  failureThreshold: 2
```

---

## Operational Playbook

### Worker is stalled

1. Check `relay_worker_last_processed_timestamp_seconds`. If stale,
   inspect the process logs for panics or unhandled errors.
2. Verify MongoDB connectivity with `db.deliveries.findOne({ status: "pending" })`.
3. Restart the worker process if the loop has deadlocked. On restart,
   expired leases will be reclaimed automatically.

### Backlog is growing

1. Check `relay_deliveries{status="pending"}` for count.
2. Inspect the oldest pending delivery:
   `db.deliveries.find({ status: "pending" }).sort({ created_at: 1 }).limit(1)`.
3. Identify the endpoint from `endpoint_id` and check for endpoint-level
   failures in `relay_deliveries_per_endpoint`.
4. If a single endpoint is unresponsive, disable it via
   `DELETE /v1/endpoints/{id}` to unblock the rest of the queue.

### Downstream returns errors

1. Examine the last 50 attempts for the endpoint:
   `db.delivery_attempts.find({ endpoint_id: ObjectId(...) }).sort({ started_at: -1 }).limit(50)`.
2. Match `http_status` values against downstream service health.
3. If failures are transient, no action needed — retries will resolve.
4. If failures persist past `max_attempts`, deliveries transition to
   `failed`. Replay manually via `POST /v1/events/{id}/replay` once the
   downstream recovers.

### Recovering after a MongoDB outage

1. Relay's worker will surface errors via `relay_worker_processing_errors_total`.
2. After MongoDB is reachable again, the worker's next poll cycle
   resumes normally. No manual intervention required.
3. Deliveries that were mid-`processing` when MongoDB went down retain
   their leases. When the leases expire, `failExhaustedLeases` and the
   claim loop handle them consistently with normal lease recovery.

---

## Metric Stability

Metric names and label keys are part of Relay's operational contract.
Renaming or removing a metric requires a deprecation cycle:

1. Add the new metric alongside the old one in a release.
2. Document the change in the release notes.
3. Remove the old metric in the following release.

Adding new label values to an existing metric is a breaking change for
any dashboard or alert that filters on the exact label set. The
maintainers should treat label additions with the same care as
renames.

---

## See Also

- [`docs/multi-endpoint-delivery.md`](multi-endpoint-delivery.md) —
  delivery lifecycle and per-endpoint semantics.
- [`docs/webhook-signing.md`](webhook-signing.md) — HMAC-SHA256
  signing protocol for outbound requests.
