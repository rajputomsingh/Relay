# Delivery Benchmarks

Reproducible benchmarks for Relay's delivery pipeline. The harness is designed to exercise the real `delivery.Worker` and the production MongoDB schema rather than a separate delivery implementation.

## Prerequisites

- Go 1.22 or later.
- A reachable MongoDB instance.
- Docker, if using the local MongoDB setup below.
- A shell that can run `go run`.

The benchmark harness reads `MONGODB_TEST_URI` by default.

## Quick Start

### 1. Start MongoDB

Run the following in PowerShell:

```powershell
docker run -d --name relay-mongo -p 27017:27017 mongo:7
```

If you already have a MongoDB instance running, you can use that instead.

### 2. Configure the connection string

```powershell
$env:MONGODB_TEST_URI = "mongodb://localhost:27017"
```

### 3. Run a benchmark

```powershell
go run ./bench --concurrency 4 --behavior fast --count 500
```

By default, raw JSON results are written to `bench/results/`.

> **Note:** Benchmark runs create temporary database state. Use a dedicated test MongoDB instance rather than a production database.

## Configuration Flags

| Flag                   | Default            | Description                                                              |
| ---------------------- | ------------------ | ------------------------------------------------------------------------ |
| `--concurrency`        | `4`                | Number of concurrent workers. Supported range: 1–32.                     |
| `--behavior`           | `fast`             | Downstream behavior to simulate.                                         |
| `--count`              | `500`              | Number of deliveries seeded before measurement.                          |
| `--max-attempts`       | `8`                | Maximum delivery retry attempts. Supported range: 1–20.                  |
| `--drain-timeout`      | `5m`               | Maximum time to wait for the backlog to drain.                           |
| `--mongo-uri`          | `MONGODB_TEST_URI` | MongoDB connection string.                                               |
| `--database`           | Generated          | Database name. A unique name is chosen when unset.                       |
| `--output`             | `bench/results`    | Directory for raw JSON results.                                          |
| `--slow-delay`         | `100ms`            | Artificial delay for the `slow` behavior.                                |
| `--transient-failures` | `50`               | Number of initial failures before `transient-503` recovers.              |
| `--warmup`             | `50`               | Deliveries processed before measurement; excluded from measured results. |
| `--quiet`              | `false`            | Suppresses periodic progress output.                                     |
| `--matrix`             | `false`            | Runs a concurrency matrix.                                               |
| `--concurrency-levels` | `1,4,8,16`         | Comma-separated concurrency levels for matrix runs.                      |

## Downstream Behaviors

| Behavior        | Simulated response                               | Purpose                                                    |
| --------------- | ------------------------------------------------ | ---------------------------------------------------------- |
| `fast`          | HTTP 204                                         | Measure maximum practical throughput.                      |
| `slow`          | HTTP 204 after a controlled delay                | Evaluate concurrency under I/O latency.                    |
| `http-500`      | HTTP 500                                         | Measure retry overhead and failure-path database activity. |
| `timeout`       | No response until the client cancels             | Evaluate request cancellation and lease behavior.          |
| `transient-503` | HTTP 503 for the first N requests, then HTTP 204 | Measure recovery and backlog drainage.                     |
| `unavailable`   | Connection refused                               | Evaluate fast-failure behavior and database pressure.      |

## Measurements

The harness is designed to collect the following measurements.

### Throughput

- **Successful throughput:** Deliveries reaching the `delivered` terminal state per second.
- **Attempted request rate:** HTTP delivery requests attempted per second.

These metrics are reported separately so that retries and failed requests do not inflate successful throughput.

### Latency

- **HTTP handler latency:** Time spent inside the mock downstream server's request handler, in milliseconds. This is not the client-observed network round-trip time.
- **Attempt duration:** `delivery_attempts.duration_ms` recorded in MongoDB.
- **Queue latency to first attempt:** `queue_latency_to_first_attempt_ms`, calculated as the earliest attempt's `started_at` minus the delivery's `created_at`.
- **Queue latency to delivery:** `queue_latency_to_delivered_ms`, calculated as the delivery's `delivered_at` minus its `created_at`.

### Drain Time

Wall-clock time from worker startup to the point at which the seeded workload reaches its terminal state.

### Backlog

Pending and processing delivery counts sampled every second during the run.

### Failure Rate

The proportion of failed deliveries among completed deliveries. Reports should include the numerator and denominator so the rate can be interpreted correctly.

## Running the Concurrency Matrix

Run the default behavior matrix across concurrency levels 1, 4, 8, and 16:

```powershell
go run ./bench --matrix --count 500 --concurrency-levels 1,4,8,16
```

By default, the matrix evaluates the `fast` and `slow` behaviors at every configured concurrency level.

To evaluate a specific behavior instead:

```powershell
go run ./bench --matrix --behavior http-500 --concurrency-levels 1,2,4
```

After a matrix run, the harness writes a consolidated summary to `bench/results/summary.md` and saves the raw JSON results for individual runs.

The summary is intended to report one row per concurrency-and-behavior combination, including:

- Drain time.
- Successful throughput.
- Attempted request rate.
- Delivered and failed counts.
- Failure rate and its denominator.
- HTTP latency percentiles: p50, p95, and p99.

## Result Files

A typical result directory looks like this:

```text
bench/results/
├── .gitkeep
├── 4_fast_<timestamp>.json
├── 4_slow_<timestamp>.json
└── summary.md
```

The exact filenames depend on the harness implementation.

Raw JSON files preserve per-run measurements. The consolidated Markdown report makes it easier to compare concurrency levels and downstream behaviors.

## Database Isolation and Cleanup

Each run is intended to use a unique database name in this format:

```text
relay_bench_<unix_nano>
```

The harness should drop the temporary database after successfully persisting the results.

If a run is interrupted, identify its database name and remove only that benchmark database. For a local MongoDB container:

```powershell
docker exec relay-mongo mongosh --eval "db.getSiblingDB('<dbname>').dropDatabase()"
```

Replace `<dbname>` with the actual benchmark database name.

**Important:** Verify the database name before dropping it. Never run cleanup commands against a production database.

## Reproducibility

For meaningful comparisons, keep the workload and environment consistent.

Record the following for every benchmark run:

- Git commit SHA.
- Go version and operating system.
- MongoDB version.
- Worker concurrency and relevant Relay configuration.
- Downstream behavior and its parameters.
- Seeded delivery count and warm-up count.
- Drain timeout and actual elapsed time.
- Successful and failed delivery counts.
- HTTP latency percentiles and throughput metrics.
- Any database metrics collected or unavailable.

Where supported by MongoDB permissions, capture `serverStatus` snapshots before and after the run. If the benchmark account cannot access these metrics, record them as unavailable instead of treating them as zero.

For more reliable comparisons:

1. Use the same delivery count and downstream behavior.
2. Keep timeout and retry settings fixed.
3. Run a warm-up before collecting measurements.
4. Repeat each scenario where practical.
5. Compare successful throughput alongside latency, failure rate, and drain time.
6. Record errors and incomplete runs rather than silently excluding them.

## Interpreting Results

To compare concurrency levels, vary `--concurrency` or use `--matrix` while keeping other parameters fixed.

Higher concurrency does not necessarily mean higher throughput. Downstream latency, retry behavior, database contention, and worker overhead can all affect results.

Interpret throughput together with p95/p99 latency, failure rates, and backlog drainage. A faster run that increases failures or produces substantially worse tail latency may not represent an operational improvement.

## Repository and Reporting Policy

- Commit the benchmark harness and its documentation.
- Keep exploratory JSON output local unless it is needed for reproducibility or review.
- Include representative raw results and a final aggregated report when publishing verified findings.
- Store the final reviewed benchmark report in `docs/benchmarks.md`.

Do not publish benchmark numbers as verified until the harness has been run successfully and the reported metrics have been checked against the actual output.
