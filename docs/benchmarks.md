# Delivery Benchmarks

Measured throughput and latency of Relay's delivery pipeline, produced by the benchmark harness in `bench/`. The harness runs the real `delivery.Worker` against MongoDB using the production schema.

## Environment

- **Go:** 1.22+
- **Operating system:** Windows
- **MongoDB:** 7, running in Docker at `localhost:27017`
- **Poll interval:** 200ms
- **Request timeout:** 10s
- **Lease duration:** 30s
- **Deliveries per baseline run:** 100
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

## Phase 1 — Failure-path validation

The same benchmark harness, delivery worker, and MongoDB schema were used to test downstream failure modes. All runs used concurrency 4, a polling interval of 200ms, a request timeout of 10s, and a lease duration of 30s.

### Results

| Behavior        | Count | Max Attempts | Drain (s) | Delivered | Failed | Attempts | HTTP p50 | Attempt p50 |
| --------------- | ----: | -----------: | --------: | --------: | -----: | -------: | -------: | ----------: |
| `http-500`      |   100 |            3 |      15.0 |         0 |    100 |      300 |      0ms |         1ms |
| `timeout`       |    20 |            2 |     101.0 |         0 |     20 |       40 |  30000ms |     10001ms |
| `transient-503` |    50 |            5 |       4.0 |        50 |      0 |       60 |      0ms |         0ms |
| `unavailable`   |    50 |            3 |      11.0 |         0 |     50 |      150 |        — |         1ms |

### Acceptance criteria

Every scenario reached a terminal state (`pending = 0`). Retry limits were respected:

- `http-500`: `attempts = 300 = 100 × 3`
- `timeout`: `attempts = 40 = 20 × 2`
- `unavailable`: `attempts = 150 = 50 × 3`
- `transient-503`: `attempts = 60`, with all 50 deliveries eventually succeeding within the five-attempt limit.

### Findings

**Timeouts dominate drain time.**

Twenty deliveries with two attempts and a 10-second request timeout took 101 seconds to drain. The simplified estimate

`count × timeout × max_attempts / concurrency`

predicts approximately 100 seconds for this run.

At a hypothetical workload of 1,000 deliveries, eight attempts, a 10-second request timeout, and concurrency 4, the same simplified model predicts:

`1000 × 10 × 8 / 4 = 20,000 seconds`, or approximately **5.6 hours**.

This is an illustrative worst-case estimate, not a measured production result. Actual drain time depends on retry backoff, polling and scheduling behavior, request cancellation, and how the worker handles exhausted attempts. Success-path benchmarks alone do not reveal this failure-recovery cost.

**`transient-503` recovered cleanly.**

The mock fails the first 10 requests, then returns HTTP 204. All 50 deliveries eventually succeeded, with 60 total attempts.

The observed retry behavior was sufficient to recover from the configured request-count-based failure period. The statement that a five-attempt retry window with approximately five-second backoff covers around 20 seconds is configuration-dependent and should be verified against the actual retry schedule.

The mock's transient behavior is request-count-based, not time-based. The harness does not yet test a destination remaining unhealthy for a specified duration. A time-based failure mode is a future refinement.

**`unavailable` records attempts without HTTP requests.**

Connection refusals produce `http.requests = 0` but `attempts.count = 150`. Failure accounting does not depend on receiving an HTTP response. A refused connection is still a failed attempt and consumes a retry.

**`server.transient_hits` counts requests, not just failures.**

In the `transient-503` run, `transient_hits = 60`, representing 10 failed requests and 50 successful requests. The counter records requests seen by the behavior, not the number of failed responses.

### Limitations

- All failure-path runs used concurrency 4. Failure-mode scaling across concurrency levels has not been measured.
- Per-attempt-number breakdowns are not recorded. The distribution of which attempt number succeeded is not visible in the current report.
- The `transient-503` failure trigger is request-count-based, not time-based.
- The `timeout` mock does not abort its handler on client disconnect. The mock handler runs for up to 30 seconds, while the delivery attempt records approximately 10 seconds due to the client timeout. A future improvement should make the mock respect `r.Context().Done()`.

## Reproducing the Results

### Environment setup

Start a local MongoDB container:

```powershell
docker run -d --name relay-mongo -p 27017:27017 mongo:7
```

Configure the connection string:

```powershell
$env:MONGODB_TEST_URI = "mongodb://localhost:27017"
```

If the container already exists, start it instead of creating another one:

```powershell
docker start relay-mongo
```

### Fast/slow baseline

Run the concurrency matrix:

```powershell
go run ./bench --matrix --count 100 --concurrency-levels 1,4 --quiet
```

The matrix writes a consolidated report to `bench/results/summary.md`, with one row per concurrency-and-behavior combination. Review the generated report and raw JSON files before updating the baseline results in this document.

### Failure-path scenarios

Run each failure behavior individually:

```powershell
go run ./bench --behavior http-500 --count 100 --max-attempts 3 --drain-timeout 10m
go run ./bench --behavior timeout --count 20 --max-attempts 2 --drain-timeout 10m
go run ./bench --behavior transient-503 --count 50 --transient-failures 10 --max-attempts 5 --drain-timeout 10m
go run ./bench --behavior unavailable --count 50 --max-attempts 3 --drain-timeout 10m
```

Run each command separately and confirm that the harness supports the supplied flags and scenario names in your current checkout.

Single-behavior runs write per-run JSON to `bench/results/` but do not produce the consolidated `summary.md`. Only `--matrix` writes the matrix summary. The JSON from each failure-path run is the source for the Phase 1 table.

## Raw Data

Per-run JSON results are written to:

```text
bench/results/<concurrency>_<behavior>_<timestamp>.json
```

Behavior names may contain hyphens, such as `http-500` and `transient-503`.

These files are excluded from version control according to `.gitignore`. Re-run the commands above to regenerate them.

For reproducible comparisons, keep the workload and environment consistent. Record the Git commit, Go and MongoDB versions, concurrency, polling interval, request timeout, lease duration, and downstream behavior alongside each run.

## Limitations (fast/slow baseline)

These figures describe the supplied benchmark results, not a universal performance guarantee.

Before using them for production capacity planning, verify:

- The reported values against the generated JSON and matrix summary.
- Whether throughput counts successful deliveries or attempted requests.
- Whether drain time includes startup, warm-up, polling delays, and final state checks.
- Whether HTTP latency percentiles are calculated from the measured request samples.
- Whether the worker polling model matches the interpretation above.
- Whether the benchmark environment and database settings match the intended deployment.
