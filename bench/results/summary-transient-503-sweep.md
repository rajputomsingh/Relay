# Benchmark Summary

Generated: 2026-10-15T14:06:05Z

Deliveries per run: 200

| Concurrency | Behavior | Drain (s) | Throughput (deliv/s) | Attempts/s | Attempted (req/s) | Delivered | Failed | Failure Rate | HTTP p50 (ms) | HTTP p95 (ms) | HTTP p99 (ms) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | transient-503 | 44.00 | 4.55 | 5.00 | 5.00 | 200 | 0 | 0.000 | 0 | 0 | 0 |
| 2 | transient-503 | 22.00 | 9.09 | 10.00 | 10.00 | 200 | 0 | 0.000 | 0 | 0 | 0 |
| 4 | transient-503 | 11.00 | 18.17 | 19.99 | 19.99 | 200 | 0 | 0.000 | 0 | 0 | 0 |
| 8 | transient-503 | 6.00 | 33.31 | 36.64 | 36.64 | 200 | 0 | 0.000 | 0 | 0 | 0 |
| 16 | transient-503 | 3.01 | 66.45 | 73.10 | 73.10 | 200 | 0 | 0.000 | 0 | 0 | 0 |
| 32 | transient-503 | 3.02 | 66.33 | 72.96 | 72.96 | 200 | 0 | 0.000 | 0 | 0 | 0 |

## Notes

Throughput at low concurrency is bounded by `PollInterval / Concurrency`. At higher concurrency, the bottleneck shifts to the MongoDB claim rate and downstream latency.
