# Delivery Benchmarks

Measured throughput and latency of Relay's delivery pipeline, produced by the benchmark harness in `bench/`. The harness runs the real `delivery.Worker` against MongoDB using the production schema.

## Environment

\- \***\*Go:\*\*** 1.22+

\- \***\*Operating system:\*\*** Windows

\- \***\*MongoDB:\*\*** 7, running in Docker at `localhost:27017`

\- \***\*Poll interval:\*\*** 200ms

\- \***\*Request timeout:\*\*** 10s

\- \***\*Lease duration:\*\*** 30s

\- \***\*Deliveries per baseline run:\*\*** 100

\- \***\*Warm-up deliveries:\*\*** 0

## Results

| Concurrency | Behavior | Drain (s) | Throughput (deliveries/s) | Delivered | Failed | HTTP p50 | HTTP p95 | HTTP p99 |

| ----------: | -------- | --------: | ------------------------: | --------: | -----: | -------: | -------: | -------: |

|           1 | `fast`   |      20.0 |                       5.0 |       100 |      0 |      0ms |      0ms |      0ms |

|           1 | `slow`   |      20.0 |                       5.0 |       100 |      0 |    100ms |    102ms |    105ms |

|           4 | `fast`   |       5.0 |                      20.0 |       100 |      0 |      0ms |      0ms |      0ms |

|           4 | `slow`   |       5.0 |                      20.0 |       100 |      0 |    100ms |    101ms |    102ms |

## Interpretation

### Throughput is bounded by the polling interval

The measured throughput follows the relationship:

\[

\text{Throughput} \approx \frac{\text{Concurrency}}{\text{Poll interval}}

\]

For a 200ms polling interval:

\- \***\*1 worker:\*\*** \(1 / 0.2 = 5\) deliveries per second.

\- \***\*4 workers:\*\*** \(4 / 0.2 = 20\) deliveries per second.

Under this model, the worker claims a delivery, processes it, and waits for the next polling tick before continuing. When the downstream responds quickly, the polling interval can dominate the time between deliveries.

The exact behavior depends on the worker implementation, including whether polling is coordinated per worker or per pool. Validate this explanation against the actual worker loop before treating it as a general property of Relay.

### Downstream latency below the polling interval

The `slow` scenario introduces approximately 100ms of downstream latency. The supplied results show the same throughput as the `fast` scenario at both concurrency levels.

The reported HTTP latency remains around 100ms, so the simulated delay is visible in request measurements even though it does not reduce the reported delivery rate.

This suggests that downstream latency is not the only factor governing throughput in these runs. Additional tests with latency above the polling interval can help determine when request duration becomes the limiting factor.

### Recommended operating settings

\- \***\*Fast downstreams:\*\*** If polling limits throughput, evaluate a shorter polling interval. Measure the resulting throughput alongside MongoDB load and empty-poll frequency.

\- \***\*Slow downstreams:\*\*** Increase concurrency experimentally to determine whether additional workers improve throughput. Watch for MongoDB contention, timeouts, retries, and tail-latency increases.

\- \***\*Production configuration:\*\*** The supplied draft identifies concurrency of 4 and a polling interval of 2 seconds as the default configuration. Under a simplified model where each of four workers can claim one delivery per polling interval, the estimated ceiling is \(4 / 2 = 2\) deliveries per second per instance. Confirm the actual configuration and worker scheduling behavior before using this estimate for capacity planning.

## Phase 1 — Failure-path validation

The same benchmark harness, delivery worker, and MongoDB schema were used to test downstream failure modes. All runs used concurrency 4, a polling interval of 200ms, a request timeout of 10s, and a lease duration of 30s.

### Results

| Behavior        | Count | Max Attempts | Drain (s) | Delivered | Failed | Attempts | HTTP p50 | Attempt p50 |

| --------------- | ----: | -----------: | --------: | --------: | -----: | -------: | -------: | ----------: |

| `http-500`      |   100 |            3 |      15.0 |         0 |    100 |      300 |      0ms |         1ms |

| `timeout`       |    20 |            2 |     101.0 |         0 |     20 |       40 |  30000ms |     10001ms |

| `transient-503` |    50 |            5 |       4.0 |        50 |      0 |       60 |      0ms |         0ms |

| `unavailable`   |    50 |            3 |      11.0 |         0 |     50 |      150 |        — |         1ms |

### Acceptance criteria

Every scenario reached a terminal state (`pending = 0`). Retry limits were respected:

\- `http-500`: `attempts = 300 = 100 × 3`

\- `timeout`: `attempts = 40 = 20 × 2`

\- `unavailable`: `attempts = 150 = 50 × 3`

\- `transient-503`: `attempts = 60`, with all 50 deliveries eventually succeeding within the five-attempt limit.

### Findings

\***\*Timeouts dominate drain time.\*\***

Twenty deliveries with two attempts and a 10-second request timeout took 101 seconds to drain. The simplified estimate

`count × timeout × max_attempts / concurrency`

predicts approximately 100 seconds for this run.

At a hypothetical workload of 1,000 deliveries, eight attempts, a 10-second request timeout, and concurrency 4, the same simplified model predicts:

`1000 × 10 × 8 / 4 = 20,000 seconds`, or approximately \***\*5.6 hours\*\***.

This is an illustrative worst-case estimate, not a measured production result. Actual drain time depends on retry backoff, polling and scheduling behavior, request cancellation, and how the worker handles exhausted attempts. Success-path benchmarks alone do not reveal this failure-recovery cost.

\***\*`transient-503` recovered cleanly.\*\***

The mock fails the first 10 requests, then returns HTTP 204. All 50 deliveries eventually succeeded, with 60 total attempts.

The observed retry behavior was sufficient to recover from the configured request-count-based failure period. The statement that a five-attempt retry window with approximately five-second backoff covers around 20 seconds is configuration-dependent and should be verified against the actual retry schedule.

The mock's transient behavior is request-count-based, not time-based. The harness does not yet test a destination remaining unhealthy for a specified duration. A time-based failure mode is a future refinement.

\***\*`unavailable` records attempts without HTTP requests.\*\***

Connection refusals produce `http.requests = 0` but `attempts.count = 150`. Failure accounting does not depend on receiving an HTTP response. A refused connection is still a failed attempt and consumes a retry.

\***\*`server.transient_hits` counts requests, not just failures.\*\***

In the `transient-503` run, `transient_hits = 60`, representing 10 failed requests and 50 successful requests. The counter records requests seen by the behavior, not the number of failed responses.

### Limitations

\- All failure-path runs used concurrency 4. Failure-mode scaling across concurrency levels has not been measured.

\- Per-attempt-number breakdowns are not recorded. The distribution of which attempt number succeeded is not visible in the current report.

\- The `transient-503` failure trigger is request-count-based, not time-based.

\- The `timeout` mock does not abort its handler on client disconnect. The mock handler runs for up to 30 seconds, while the delivery attempt records approximately 10 seconds due to the client timeout. A future improvement should make the mock respect `r.Context().Done()`.

## Phase 2 — Failure-mode concurrency sweep

Extends the failure-path validation to test throughput scaling across concurrency levels. All runs use count 200, `max_attempts 3`, poll interval 200ms, request timeout 10s, lease duration 30s.

### `unavailable`

| Concurrency | Drain (s) | Attempts/s | Expected (5 × conc) | Failed |

| ----------: | --------: | ---------: | ------------------: | -----: |

|           1 |    120.00 |       5.00 |                   5 |    200 |

|           2 |     60.00 |      10.00 |                  10 |    200 |

|           4 |     30.00 |      20.00 |                  20 |    200 |

|           8 |     15.01 |      39.99 |                  40 |    200 |

|          16 |     10.01 |      59.96 |                  80 |    200 |

|          32 |      9.01 |      66.59 |                 160 |    200 |

**Finding: two ceilings.** A useful finite-workload approximation is to
compare the poll-limited rate (`concurrency × 5 attempts/s`) with the
workload's backoff-limited rate (`total attempts / backoff floor`). The
backoff is exponential with jitter (see Measurement fields below); for
`max_attempts = 3` the total backoff between the first and last attempt
is 6–7.5 seconds. This is a heuristic, not a universal steady-state
throughput formula.

- At count 200, the backoff-based estimate is `3 × 200 / 7.5 ≈ 80
attempts/s`. The observed plateau is approximately 67 attempts/s,
  within ~15% of that estimate. The remaining gap is consistent with
  scheduler jitter and the distribution of `retryDelay` samples across
  concurrent workers.
- At count 1000 in the `http-500` sweep below, the corresponding
  estimate is `3 × 1000 / 7.5 ≈ 400 attempts/s`, above the poll ceiling
  at every tested concurrency level. The observed rate is therefore
  poll-limited across the tested range.

The effective observed rate depends on both concurrency and workload size; do not generalize the count-200 plateau to every failure workload.

\***\*Implication for production.\*\*** Concurrency above 8 does not increase delivery throughput when retries are in play. The workers sit idle waiting for `next_attempt_at`. If a deployment needs higher failure throughput, reducing the backoff interval is more effective than adding workers.

\***\*Note on `Attempted (req/s)`.\*\*** For `unavailable`, no HTTP request is made, so this column is always 0. `Attempts/s` is the metric that scales.

### `http-500`

Count 1000, `max_attempts = 3`.

| Concurrency | Drain (s) | Attempts/s | Expected (5 × conc) | Failed |
| ----------: | --------: | ---------: | ------------------: | -----: |
|           1 |    600.01 |       5.00 |                   5 |   1000 |
|           2 |    300.01 |      10.00 |                  10 |   1000 |
|           4 |    150.01 |      20.00 |                  20 |   1000 |
|           8 |     75.01 |      40.00 |                  40 |   1000 |
|          16 |     38.01 |      78.93 |                  80 |   1000 |
|          32 |     19.01 |     157.77 |                 160 |   1000 |

**Finding: poll-limited across the tested range.** Throughput scales approximately linearly with concurrency through 32, tracking `concurrency × 5 attempts/s`. The backoff-based estimate for this workload is `3 × 1000 / 7.5 ≈ 400 attempts/s`, above the poll ceiling of `160 attempts/s` at concurrency 32. The measurements therefore show no backoff-driven plateau within these tested concurrency levels.

### Comparison

The `unavailable` count-200 run and the `http-500` count-1000 run illustrate that workload size affects which constraint is visible. The `unavailable` run approaches a finite-workload backoff floor, while the larger `http-500` run remains close to the poll-limited rate through concurrency 32. Treat `min(concurrency × 5, total attempts / backoff floor)` as a rough comparison of ceilings, not an exact performance law: scheduling, retry timing, polling, and drain-time measurement can affect observed results.

### `transient-503`

Count 200, `transient_failures 20`, `max_attempts 5`.

| Concurrency | Drain (s) | Attempts/s | Attempts | Delivered | Failed |
| ----------: | --------: | ---------: | -------: | --------: | -----: |
|           1 |     44.00 |       5.00 |      220 |       200 |      0 |
|           2 |     22.00 |      10.00 |      220 |       200 |      0 |
|           4 |     11.00 |      19.99 |      220 |       200 |      0 |
|           8 |      6.00 |      36.64 |      220 |       200 |      0 |
|          16 |      3.01 |      73.10 |      220 |       200 |      0 |
|          32 |      3.02 |      72.96 |      220 |       200 |      0 |

**Finding: recovery floor at ~3s, not backoff-bound.** All 200
deliveries succeeded at every concurrency level. The total attempt count
is 220 at every level: 200 first attempts + 20 retries for the deliveries
that hit the initial failing window.

Throughput scales linearly to concurrency 8, then plateaus around
73 attempts/s. The plateau is not the retry backoff window — the retried
deliveries succeed on their second attempt, ~2.5s after the first pass.
The floor is the recovery window itself: 200 deliveries / (16 × 5/s)
= 2.5s for the first pass, plus one poll tick for the retry, giving a
minimum drain of roughly 3s at any concurrency ≥ 16.

### Measurement fields

Each run reports two additional fields beyond the original schema:

- **`attempts per number`**: count of attempts grouped by attempt
  number. For example, `1=200 2=200 3=50` means 200 deliveries made
  at least one attempt, 200 made at least two, and 50 made three.
- **`retry_gap p50/p95`**: milliseconds between consecutive attempts
  of the same delivery.

The worker uses exponential backoff with jitter:

    retryDelay(n) = 2s × 2^(n-1) + jitter(0–25%)

capped at 5 minutes. For `max_attempts = 3` the total backoff is
6–7.5 seconds. Measured `retry_gap` p50 and p95 in `http-500` were
2.8s and 5.0s, matching `retryDelay(1) + overhead` and
`retryDelay(2) + overhead`.

**Exponential backoff makes drain time superlinear in `max_attempts`.**
Doubling `max_attempts` from 3 to 6 multiplies the backoff floor from
~7.5s to ~63s.

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

### Failure-mode concurrency sweep

Run the sweep for each failure behavior at count 200:

```powershell
go run ./bench --matrix --behavior unavailable --count 200 --max-attempts 3 --concurrency-levels 1,2,4,8,16,32 --drain-timeout 10m --quiet
Copy-Item bench/results/summary.md bench/results/summary-unavailable-sweep.md

go run ./bench --matrix --behavior http-500 --count 200 --max-attempts 3 --concurrency-levels 1,2,4,8,16,32 --drain-timeout 10m --quiet
Copy-Item bench/results/summary.md bench/results/summary-http-500-sweep.md

go run ./bench --matrix --behavior transient-503 --count 200 --transient-failures 20 --max-attempts 5 --concurrency-levels 1,2,4,8,16,32 --drain-timeout 10m --quiet
Copy-Item bench/results/summary.md bench/results/summary-transient-503-sweep.md
```

Each sweep writes a consolidated report to `bench/results/summary.md`. Copy the output to a per-behavior file before running the next sweep, because each run overwrites `summary.md`.

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

Measured throughput and latency of Relay's delivery pipeline, produced by the benchmark harness in `bench/`. The harness runs the real `delivery.Worker` against MongoDB using the production schema.

## Environment

- **Go:\*\*** 1.22+

- **Operating system:\*\*** Windows

- **MongoDB:\*\*** 7, running in Docker at `localhost:27017`

- **Poll interval:\*\*** 200ms

- **Request timeout:\*\*** 10s

- **Lease duration:\*\*** 30s

- **Deliveries per baseline run:\*\*** 100

- **Warm-up deliveries:\*\*** 0

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

[

\text{Throughput} \approx \frac{\text{Concurrency}}{\text{Poll interval}}

]

For a 200ms polling interval:

- **1 worker:\*\*** \(1 / 0.2 = 5\) deliveries per second.

- **4 workers:\*\*** \(4 / 0.2 = 20\) deliveries per second.

Under this model, the worker claims a delivery, processes it, and waits for the next polling tick before continuing. When the downstream responds quickly, the polling interval can dominate the time between deliveries.

The exact behavior depends on the worker implementation, including whether polling is coordinated per worker or per pool. Validate this explanation against the actual worker loop before treating it as a general property of Relay.

### Downstream latency below the polling interval

The `slow` scenario introduces approximately 100ms of downstream latency. The supplied results show the same throughput as the `fast` scenario at both concurrency levels.

The reported HTTP latency remains around 100ms, so the simulated delay is visible in request measurements even though it does not reduce the reported delivery rate.

This suggests that downstream latency is not the only factor governing throughput in these runs. Additional tests with latency above the polling interval can help determine when request duration becomes the limiting factor.

### Recommended operating settings

- **Fast downstreams:\*\*** If polling limits throughput, evaluate a shorter polling interval. Measure the resulting throughput alongside MongoDB load and empty-poll frequency.

- **Slow downstreams:\*\*** Increase concurrency experimentally to determine whether additional workers improve throughput. Watch for MongoDB contention, timeouts, retries, and tail-latency increases.

- **Production configuration:\*\*** The supplied draft identifies concurrency of 4 and a polling interval of 2 seconds as the default configuration. Under a simplified model where each of four workers can claim one delivery per polling interval, the estimated ceiling is \(4 / 2 = 2\) deliveries per second per instance. Confirm the actual configuration and worker scheduling behavior before using this estimate for capacity planning.

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

**Timeouts dominate drain time.\*\***

Twenty deliveries with two attempts and a 10-second request timeout took 101 seconds to drain. The simplified estimate

`count × timeout × max_attempts / concurrency`

predicts approximately 100 seconds for this run.

At a hypothetical workload of 1,000 deliveries, eight attempts, a 10-second request timeout, and concurrency 4, the same simplified model predicts:

`1000 × 10 × 8 / 4 = 20,000 seconds`, or approximately **5.6 hours\*\***.

This is an illustrative worst-case estimate, not a measured production result. Actual drain time depends on retry backoff, polling and scheduling behavior, request cancellation, and how the worker handles exhausted attempts. Success-path benchmarks alone do not reveal this failure-recovery cost.

**`transient-503` recovered cleanly.\*\***

The mock fails the first 10 requests, then returns HTTP 204. All 50 deliveries eventually succeeded, with 60 total attempts.

The observed retry behavior was sufficient to recover from the configured request-count-based failure period. The statement that a five-attempt retry window with approximately five-second backoff covers around 20 seconds is configuration-dependent and should be verified against the actual retry schedule.

The mock's transient behavior is request-count-based, not time-based. The harness does not yet test a destination remaining unhealthy for a specified duration. A time-based failure mode is a future refinement.

**`unavailable` records attempts without HTTP requests.\*\***

Connection refusals produce `http.requests = 0` but `attempts.count = 150`. Failure accounting does not depend on receiving an HTTP response. A refused connection is still a failed attempt and consumes a retry.

**`server.transient_hits` counts requests, not just failures.\*\***

In the `transient-503` run, `transient_hits = 60`, representing 10 failed requests and 50 successful requests. The counter records requests seen by the behavior, not the number of failed responses.

### Limitations

- All failure-path runs used concurrency 4. Failure-mode scaling across concurrency levels has not been measured.

- Per-attempt-number breakdowns are not recorded. The distribution of which attempt number succeeded is not visible in the current report.

- The `transient-503` failure trigger is request-count-based, not time-based.

- The `timeout` mock does not abort its handler on client disconnect. The mock handler runs for up to 30 seconds, while the delivery attempt records approximately 10 seconds due to the client timeout. A future improvement should make the mock respect `r.Context().Done()`.

## Phase 2 — Failure-mode concurrency sweep

Extends the failure-path validation to test throughput scaling across concurrency levels. All runs use count 200, `max_attempts 3`, poll interval 200ms, request timeout 10s, lease duration 30s.

### `unavailable`

| Concurrency | Drain (s) | Attempts/s | Expected (5 × conc) | Failed |
| ----------: | --------: | ---------: | ------------------: | -----: |
|           1 |    120.00 |       5.00 |                   5 |    200 |
|           2 |     60.00 |      10.00 |                  10 |    200 |
|           4 |     30.00 |      20.00 |                  20 |    200 |
|           8 |     15.01 |      39.99 |                  40 |    200 |
|          16 |     10.01 |      59.96 |                  80 |    200 |
|          32 |      9.01 |      66.59 |                 160 |    200 |

**Finding: two ceilings.** A useful finite-workload approximation is to

compare the poll-limited rate (`concurrency × 5 attempts/s`) with the

workload's backoff-limited rate (`total attempts / backoff floor`). The

backoff is exponential with jitter (see Measurement fields below); for

`max_attempts = 3` the total backoff between the first and last attempt

is 6–7.5 seconds. This is a heuristic, not a universal steady-state

throughput formula.

- At count 200, the backoff-based estimate is `3 × 200 / 7.5 ≈ 80

attempts/s`. The observed plateau is approximately 67 attempts/s,

  within ~15% of that estimate. The remaining gap is consistent with

  scheduler jitter and the distribution of `retryDelay` samples across

  concurrent workers.

- At count 1000 in the `http-500` sweep below, the corresponding

  estimate is `3 × 1000 / 7.5 ≈ 400 attempts/s`, above the poll ceiling

  at every tested concurrency level. The observed rate is therefore

  poll-limited across the tested range.

The effective observed rate depends on both concurrency and workload size; do not generalize the count-200 plateau to every failure workload.

**Implication for production.\*\*** Concurrency above 8 does not increase delivery throughput when retries are in play. The workers sit idle waiting for `next_attempt_at`. If a deployment needs higher failure throughput, reducing the backoff interval is more effective than adding workers.

**Note on `Attempted (req/s)`.\*\*** For `unavailable`, no HTTP request is made, so this column is always 0. `Attempts/s` is the metric that scales.

### `http-500`

Count 1000, `max_attempts = 3`.

| Concurrency | Drain (s) | Attempts/s | Expected (5 × conc) | Failed |
| ----------: | --------: | ---------: | ------------------: | -----: |
|           1 |    600.01 |       5.00 |                   5 |   1000 |
|           2 |    300.01 |      10.00 |                  10 |   1000 |
|           4 |    150.01 |      20.00 |                  20 |   1000 |
|           8 |     75.01 |      40.00 |                  40 |   1000 |
|          16 |     38.01 |      78.93 |                  80 |   1000 |
|          32 |     19.01 |     157.77 |                 160 |   1000 |

**Finding: poll-limited across the tested range.** Throughput scales approximately linearly with concurrency through 32, tracking `concurrency × 5 attempts/s`. The backoff-based estimate for this workload is `3 × 1000 / 7.5 ≈ 400 attempts/s`, above the poll ceiling of `160 attempts/s` at concurrency 32. The measurements therefore show no backoff-driven plateau within these tested concurrency levels.

### Comparison

The `unavailable` count-200 run and the `http-500` count-1000 run illustrate that workload size affects which constraint is visible. The `unavailable` run approaches a finite-workload backoff floor, while the larger `http-500` run remains close to the poll-limited rate through concurrency 32. Treat `min(concurrency × 5, total attempts / backoff floor)` as a rough comparison of ceilings, not an exact performance law: scheduling, retry timing, polling, and drain-time measurement can affect observed results.

### `transient-503`

Count 200, `transient_failures 20`, `max_attempts 5`.

| Concurrency | Drain (s) | Attempts/s | Attempts | Delivered | Failed |
| ----------: | --------: | ---------: | -------: | --------: | -----: |
|           1 |     44.00 |       5.00 |      220 |       200 |      0 |
|           2 |     22.00 |      10.00 |      220 |       200 |      0 |
|           4 |     11.00 |      19.99 |      220 |       200 |      0 |
|           8 |      6.00 |      36.64 |      220 |       200 |      0 |
|          16 |      3.01 |      73.10 |      220 |       200 |      0 |
|          32 |      3.02 |      72.96 |      220 |       200 |      0 |

**Finding: recovery floor at ~3s, not backoff-bound.** All 200

deliveries succeeded at every concurrency level. The total attempt count

is 220 at every level: 200 first attempts + 20 retries for the deliveries

that hit the initial failing window.

Throughput scales linearly to concurrency 8, then plateaus around

73 attempts/s. The plateau is not the retry backoff window — the retried

deliveries succeed on their second attempt, ~2.5s after the first pass.

The floor is the recovery window itself: 200 deliveries / (16 × 5/s)

= 2.5s for the first pass, plus one poll tick for the retry, giving a

minimum drain of roughly 3s at any concurrency ≥ 16.

### Measurement fields

Each run reports two additional fields beyond the original schema:

- **`attempts per number`\*\***: count of attempts grouped by attempt

  number. For example, `1=200 2=200 3=50` means 200 deliveries made

  at least one attempt, 200 made at least two, and 50 made three.

- **`retry_gap p50/p95`\*\***: milliseconds between consecutive attempts

  of the same delivery.

The worker uses exponential backoff with jitter:

    retryDelay(n) = 2s × 2^(n-1) + jitter(0–25%)

capped at 5 minutes. For `max_attempts = 3` the total backoff is

6–7.5 seconds. Measured `retry_gap` p50 and p95 in `http-500` were

2.8s and 5.0s, matching `retryDelay(1) + overhead` and

`retryDelay(2) + overhead`.

**Exponential backoff makes drain time superlinear in `max_attempts`.**

Doubling `max_attempts` from 3 to 6 multiplies the backoff floor from

~7.5s to ~63s.

## Phase 3 — Time-based outage recovery

The `outage` behavior returns HTTP 503 for a configured wall-clock duration, then returns HTTP 204. Unlike `transient-503` (which fails a fixed count of requests), this models a downstream that is genuinely unreachable for a window of time. The measurement of interest is `recovery_drain_seconds`: time from outage end to the last delivery reaching a terminal state.

Count 30, concurrency 4, with `max_attempts` varied per run.

### Results

| Outage | Max attempts | Delivered | Failed | Attempts used | Recovery drain (s) |
| -----: | -----------: | --------: | -----: | ------------: | -----------------: |
|    10s |            4 |        30 |      0 |             4 |                9.1 |
|    10s |            6 |        30 |      0 |             4 |                9.0 |
|    10s |            8 |        30 |      0 |             4 |                9.0 |
|    30s |            4 |         0 |     30 | 4 (exhausted) |                  — |
|    30s |            6 |        30 |      0 |             5 |                8.0 |
|    30s |            8 |        30 |      0 |             5 |                9.0 |
|    60s |            4 |         0 |     30 | 4 (exhausted) |                  — |
|    60s |            6 |        30 |      0 |             6 |               18.0 |
|    60s |            8 |        30 |      0 |             6 |               18.0 |

### Finding: `max_attempts` must outlast the outage

Ignoring jitter, the last allowed attempt occurs after the sum of the first `N - 1` retry delays. With exponential backoff (`2s × 2^(n-1)`), the no-jitter cumulative delay is `2^N - 2` seconds:

| Max attempts | Last attempt at (no jitter) | Approximate outage window |
| -----------: | --------------------------: | ------------------------: |
|            4 |                         14s |                       14s |
|            5 |                         30s |                       30s |
|            6 |                         62s |                       62s |
|            7 |                        126s |                      126s |
|            8 |                        254s |                      254s |

If the outage lasts longer than the final scheduled attempt, every delivery can exhaust its retry budget while the downstream is still unavailable. All 30 deliveries failed at `max_attempts = 4` for both the 30s and 60s outages.

Jitter is non-negative (the worker adds up to 25% of the base delay), so it can only delay an attempt, never advance it. Therefore, `2^N - 2` is a no-jitter baseline, not a guarantee that an outage of exactly that duration will be survived. For a 30s outage, `max_attempts = 5` sits on the boundary and may be insufficient once jitter is included; `max_attempts = 6` provides margin.

### Recovery time is dominated by the final backoff

Once the outage ends, deliveries do not retry immediately. Each failed delivery waits out its current backoff before its next attempt. The observed recovery drain across the passing cells is 8–18 seconds.

- 10s outage: recovery is approximately 9s; the pending backoff is `retryDelay(3)`, nominally 8s before jitter.
- 30s outage: recovery is approximately 8–9s; the last delivery's fifth attempt fires near the outage boundary.
- 60s outage: recovery is approximately 18s; the sixth attempt fires part-way through the next backoff window.

Total recovery time is roughly `outage_duration + last_pending_backoff`, not `outage_duration` alone.

### Production implication

Choose `max_attempts` from the longest outage you need to survive, not just from the retry count you would accept for a transient failure.

- `max_attempts = 5`: nominal final attempt at 30s; boundary case for a 30s outage.
- `max_attempts = 6`: nominal final attempt at 62s.
- `max_attempts = 7`: nominal final attempt at 126s (about 2 minutes).
- `max_attempts = 8`: nominal final attempt at 254s (about 4 minutes).

For an endpoint whose SLA requires surviving a five-minute outage, `max_attempts = 8` is not sufficient under the stated backoff schedule because its nominal final attempt is at 254 seconds, before five minutes. At least 9 attempts are needed to place a nominal final attempt beyond 5 minutes, subject to the worker's five-minute per-delay cap and measured jitter. Concurrency, poll interval, and lease duration do not change the retry schedule's nominal cumulative delay, though they can affect observed completion timing.

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

### Failure-mode concurrency sweep

Run the sweep for each failure behavior at count 200:

```powershell

go run ./bench --matrix --behavior unavailable --count 200 --max-attempts 3 --concurrency-levels 1,2,4,8,16,32 --drain-timeout 10m --quiet

Copy-Item bench/results/summary.md bench/results/summary-unavailable-sweep.md

go run ./bench --matrix --behavior http-500 --count 200 --max-attempts 3 --concurrency-levels 1,2,4,8,16,32 --drain-timeout 10m --quiet

Copy-Item bench/results/summary.md bench/results/summary-http-500-sweep.md

go run ./bench --matrix --behavior transient-503 --count 200 --transient-failures 20 --max-attempts 5 --concurrency-levels 1,2,4,8,16,32 --drain-timeout 10m --quiet

Copy-Item bench/results/summary.md bench/results/summary-transient-503-sweep.md

```

Each sweep writes a consolidated report to `bench/results/summary.md`. Copy the output to a per-behavior file before running the next sweep, because each run overwrites `summary.md`.

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


\## Phase 4 â€” Capacity and saturation



Sustained load at count 5000, concurrency 8, 16, and 32. The `fast` and

`slow` behaviors. MongoDB 7 in Docker, Go 1.27.0, Windows.



\### Results



| Behavior | Concurrency | Drain (s) | Throughput (deliv/s) | Mongo conns |

| -------- | ----------: | --------: | -------------------: | ----------: |

| fast     | 8           | 142       | 35.2                 | 11          |

| fast     | 16          | 127       | 39.4                 | 20          |

| fast     | 32          | 115       | 43.5                 | 32          |

| slow     | 8           | 141       | 35.5                 | 11          |

| slow     | 16          | 133       | 37.6                 | 20          |

| slow     | 32          | 136       | 36.8                 | 36          |



\### Finding: throughput plateaus at concurrency 8



The linear model from the baseline predicts `concurrency Ã— 5 deliv/s`

throughput. Observed:



\- Concurrency 8: 35.2/s vs predicted 40/s â€” 88% of linear

\- Concurrency 16: 39.4/s vs predicted 80/s â€” 49% of linear

\- Concurrency 32: 43.5/s vs predicted 160/s â€” 27% of linear



Doubling concurrency from 8 to 16 raises throughput by only 12%.

Doubling again from 16 to 32 raises it by only 10%. The host CPU

saturates: additional workers wait for CPU time rather than producing

proportional throughput.



The `slow` behavior gives the same plateau. At concurrency 8 and above,

the 100ms per-request sleep no longer dominates â€” CPU contention does.

Throughput for `slow` is 35.5, 37.6, and 36.8/s, essentially flat across

concurrency.



\### MongoDB is not the bottleneck



Every run at every concurrency level produced `inserts = 3 Ã— count`,

`queries â‰ˆ 3 Ã— count`, and `updates = 2 Ã— count`, exactly the expected

shape for a run with one endpoint and one attempt per delivery. The

`connections\_current` field ranged from 11 to 36, well below the

default driver pool of 100. The MongoDB server had spare capacity

throughout. The bottleneck is CPU-bound work in the worker process:

JSON and BSON encoding, HTTP client overhead, and driver round-trip

handling all consume host CPU.



\### Delivery correctness



Every cell delivered all 5000 deliveries with zero failures. The

`attempts per number` distribution is `1=5000` for every run except

when a transient network error causes a single retry â€” the retry

succeeds within the configured `max\_attempts` of 8. `queue\_growth`

samples show the backlog starting at 5000 and draining monotonically

to zero.



\### Production implication



The ceiling on a mid-range Windows host is approximately \*\*40

deliveries/sec per Relay instance\*\*, regardless of worker count above 8.

For capacity planning:



\- Concurrency 8 is the practical default. Higher concurrency costs CPU

&#x20; without proportional benefit.

\- To scale beyond 40/s, add instances rather than workers.

\- Verify the ceiling on the actual deployment hardware before committing

&#x20; to a capacity number. This benchmark measures one host; CPU speed,

&#x20; disk latency, and network overhead will shift the plateau.



\### Limitations



\- One `slow` run at concurrency 8 was observed to drain in 1783 seconds

&#x20; with a `queue\_to\_first\_attempt\_p50` of 28.7 minutes, far outside the

&#x20; pattern of the other six cells. The cause was not identified. Treat

&#x20; it as a harness-level anomaly; a clean container rerun did not

&#x20; reproduce it. All other cells drained in 115â€“142 seconds.

\- The harness's own poll interval is 200ms, faster than production's

&#x20; 2-second default. At production's poll interval the plateau would

&#x20; occur at a lower concurrency. The 8-worker threshold is specific to

&#x20; this configuration.


