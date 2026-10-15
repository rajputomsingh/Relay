# Benchmark Summary

Generated: 2026-10-15T13:57:39Z

Deliveries per run: 200

| Concurrency | Behavior | Drain (s) | Throughput (deliv/s) | Attempts/s | Attempted (req/s) | Delivered | Failed | Failure Rate | HTTP p50 (ms) | HTTP p95 (ms) | HTTP p99 (ms) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | unavailable | 120.00 | 0.00 | 5.00 | 0.00 | 0 | 200 | 1.000 | 0 | 0 | 0 |
| 2 | unavailable | 60.00 | 0.00 | 10.00 | 0.00 | 0 | 200 | 1.000 | 0 | 0 | 0 |
| 4 | unavailable | 30.00 | 0.00 | 20.00 | 0.00 | 0 | 200 | 1.000 | 0 | 0 | 0 |
| 8 | unavailable | 15.01 | 0.00 | 39.99 | 0.00 | 0 | 200 | 1.000 | 0 | 0 | 0 |
| 16 | unavailable | 10.01 | 0.00 | 59.96 | 0.00 | 0 | 200 | 1.000 | 0 | 0 | 0 |
| 32 | unavailable | 9.01 | 0.00 | 66.59 | 0.00 | 0 | 200 | 1.000 | 0 | 0 | 0 |

## Notes

Throughput at low concurrency is bounded by `PollInterval / Concurrency`. At higher concurrency, the bottleneck shifts to the MongoDB claim rate and downstream latency.
