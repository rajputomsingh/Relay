## Context

Per-endpoint health (GET /v1/endpoints/{id}/health) reports the health of a webhook destination. It does not report whether the Relay service itself is healthy.

Service-level monitoring and orchestration need a separate endpoint.

## Scope

Add an unauthenticated GET /v1/health endpoint.

Proposed response:

``json
{
  "status": "ok",
  "mongo": "ok",
  "worker": "ok",
  "version": "dev",
  "uptime_seconds": 3612
}
``

Define exact status semantics from observable service state:

- status: ok, degraded, or down.
- mongo: ok or unreachable, using a bounded ping timeout.
- worker: ok, stale, or disabled, based on actual worker state.
- version: build version, with a sensible development default.
- uptime_seconds: elapsed time since process start.

Return HTTP 200 for ok or degraded, and 503 for down, if supported by the final design.

## Acceptance Criteria

- [ ] Endpoint is served without authentication.
- [ ] MongoDB check has a hard timeout.
- [ ] Worker state reflects actual state, including when the worker never started.
- [ ] OpenAPI is updated.
- [ ] Integration tests cover healthy service, unreachable MongoDB, and worker-not-started states.
- [ ] README explains how to use the endpoint for service monitoring.

## Hints

Reuse the existing MongoDB ping pattern where appropriate. Track the worker's last successful poll if that is the most reliable signal.

**Size:** M (half day)
