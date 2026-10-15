# Benchmark Summary

Generated: 2026-10-15T11:31:01Z

Deliveries per run: 100

| Concurrency | Behavior | Drain (s) | Throughput (deliv/s) | Attempted (req/s) | Delivered | Failed | Failure Rate | HTTP p50 (ms) | HTTP p95 (ms) | HTTP p99 (ms) |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | fast | 20.03 | 4.99 | 4.99 | 100 | 0 | 0.000 | 0 | 0 | 0 |
| 1 | slow | 20.00 | 5.00 | 5.00 | 100 | 0 | 0.000 | 100 | 102 | 105 |
| 4 | fast | 5.01 | 19.98 | 19.98 | 100 | 0 | 0.000 | 0 | 0 | 0 |
| 4 | slow | 5.01 | 19.97 | 19.97 | 100 | 0 | 0.000 | 100 | 101 | 102 |

## Notes

Throughput at low concurrency is bounded by `PollInterval / Concurrency`. At higher concurrency, the bottleneck shifts to the MongoDB claim rate and downstream latency.
