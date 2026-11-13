## Context

The endpoint-health response reports attempt counts, success rate, and consecutive failures, but does not report delivery latency. Delivery attempts record start and finish timestamps, which may support latency calculations.

## Scope

Add these additive fields to endpoint health:

```json
{
  "p50_latency_ms": 42,
  "p95_latency_ms": 187,
  "p99_latency_ms": 512
}
```

Calculate percentiles over the same one-hour window used for attempts_1h, using successful attempts (outcome = delivered) only. Return null when there are no successful attempts in the window.

## Acceptance Criteria

- [ ] All three fields are present in the response.
- [ ] Fields are null when no successful attempts exist in the window.
- [ ] Calculation is integrated into the existing aggregation where feasible; avoid unnecessary database round trips.
- [ ] Integration tests seed known latencies and assert percentile behavior.
- [ ] OpenAPI marks the fields nullable.
- [ ] Node SDK types are updated.
- [ ] PR description includes a response example.

## Hints

MongoDB 7.0+ provides . Confirm the deployed MongoDB version and aggregation semantics before relying on it. Compute latency from finished_at minus started_at in milliseconds and filter to delivered outcomes.

Keep existing response fields unchanged; these additions must be backward-compatible.

**Size:** M (half day)
