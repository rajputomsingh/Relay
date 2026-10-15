# Benchmark Summary

Generated: 2026-10-15T13:18:57Z

Deliveries per run: 1000

| Concurrency | Behavior | Drain (s) | Throughput (deliv/s) | Attempted (req/s) | Delivered | Failed | Failure Rate | HTTP p50 (ms) | HTTP p95 (ms) | HTTP p99 (ms) |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | http-500 | 600.01 | 0.00 | 5.00 | 0 | 1000 | 1.000 | 0 | 0 | 0 |
| 2 | http-500 | 300.01 | 0.00 | 10.00 | 0 | 1000 | 1.000 | 0 | 0 | 0 |
| 4 | http-500 | 150.01 | 0.00 | 20.00 | 0 | 1000 | 1.000 | 0 | 0 | 0 |
| 8 | http-500 | 75.01 | 0.00 | 40.00 | 0 | 1000 | 1.000 | 0 | 0 | 0 |
| 16 | http-500 | 38.01 | 0.00 | 78.93 | 0 | 1000 | 1.000 | 0 | 0 | 0 |
| 32 | http-500 | 19.01 | 0.00 | 157.77 | 0 | 1000 | 1.000 | 0 | 0 | 0 |

## Notes

Throughput at low concurrency is bounded by `PollInterval / Concurrency`. At higher concurrency, the bottleneck shifts to the MongoDB claim rate and downstream latency.
