# Delivery Benchmarks

Measured throughput and latency of Relay's delivery pipeline, produced by the benchmark harness in `bench/`. The harness runs the real `delivery.Worker` against MongoDB using the production schema.

## Environment

- **Go:** 1.22+
- **Operating system:** Windows
- **MongoDB:** 7, running in Docker at `localhost:27017`
- **Poll interval:** 200ms
- **Request timeout:** 10s
- **Lease duration:** 30s
- **Deliveries per run:** 100
- **Warm-up deliveries:** 0

## Results

| Concurrency | Behavior | Drain (s) | Throughput (deliveries/s) | Delivered | Failed | HTTP p50 | HTTP p95 | HTTP p99 |
| ----------: | -------- | --------: | ------------------------: | --------: | -----: | -------: | -------: | -------: |
|           1 | `fast`   |      20.0 |                       5.0 |       100 |      0 |      0ms |      0ms |      0ms |
|           1 | `slow`   |      20.0 |                       5.0 |       100 |      0 |    100ms |    102ms |    105ms |
|           4 | `fast`   |       5.0 |                      20.0 |       100 |      0 |      0ms |      0ms |      0ms |
|           4 | `slow`   |       5.0 |                      20.0 |       100 |      0 |    100ms |    101ms |    102ms |

## Interpretation

### Throughput is bounded by the polling interval

The measured throughput follows the relationship:

\[
\text{Throughput} \approx \frac{\text{Concurrency}}{\text{Poll interval}}
\]

For a 200ms polling interval:

- **1 worker:** \(1 / 0.2 = 5\) deliveries per second.
- **4 workers:** \(4 / 0.2 = 20\) deliveries per second.

Under this model, the worker claims a delivery, processes it, and waits for the next polling tick before continuing. When the downstream responds quickly, the polling interval can dominate the time between deliveries.

The exact behavior depends on the worker implementation, including whether polling is coordinated per worker or per pool. Validate this explanation against the actual worker loop before treating it as a general property of Relay.

### Downstream latency below the polling interval

The `slow` scenario introduces approximately 100ms of downstream latency. The supplied results show the same throughput as the `fast` scenario at both concurrency levels.

The reported HTTP latency remains around 100ms, so the simulated delay is visible in request measurements even though it does not reduce the reported delivery rate.

This suggests that downstream latency is not the only factor governing throughput in these runs. Additional tests with latency above the polling interval can help determine when request duration becomes the limiting factor.

### Recommended operating settings

- **Fast downstreams:** If polling limits throughput, evaluate a shorter polling interval. Measure the resulting throughput alongside MongoDB load and empty-poll frequency.
- **Slow downstreams:** Increase concurrency experimentally to determine whether additional workers improve throughput. Watch for MongoDB contention, timeouts, retries, and tail-latency increases.
- **Production configuration:** The supplied draft identifies concurrency of 4 and a polling interval of 2 seconds as the default configuration. Under a simplified model where each of four workers can claim one delivery per polling interval, the estimated ceiling is \(4 / 2 = 2\) deliveries per second per instance. Confirm the actual configuration and worker scheduling behavior before using this estimate for capacity planning.

## Reproducing the Results

Start a local MongoDB container:

```powershell
docker run -d --name relay-mongo -p 27017:27017 mongo:7
```

Configure the connection string:

```powershell
$env:MONGODB_TEST_URI = "mongodb://localhost:27017"
```

Run the concurrency matrix:

```powershell
go run ./bench --matrix --count 100 --concurrency-levels 1,4 --quiet
```

The matrix writes a consolidated report to `bench/results/summary.md`, with one row per concurrency-and-behavior combination.

Review the generated report and raw JSON files to confirm the results before updating this document.

## Raw Data

Per-run JSON results are written to:

```text
bench/results/<concurrency>_<behavior>_<timestamp>.json
```

These files are excluded from version control according to `.gitignore`. Re-run the benchmark command above to regenerate them.

For reproducible comparisons, keep the workload and environment consistent. Record the Git commit, Go and MongoDB versions, concurrency, polling interval, request timeout, lease duration, and downstream behavior alongside each run.

## Limitations

These figures describe the supplied benchmark results, not a universal performance guarantee.

Before using them for production capacity planning, verify:

- The reported values against the generated JSON and matrix summary.
- Whether throughput counts successful deliveries or attempted requests.
- Whether drain time includes startup, warm-up, polling delays, and final state checks.
- Whether HTTP latency percentiles are calculated from the measured request samples.
- Whether the worker polling model matches the interpretation above.
- Whether the benchmark environment and database settings match the intended deployment.
