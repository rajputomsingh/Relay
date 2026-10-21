<p align="center">
  <img width="120" height="120" src="https://github.com/user-attachments/assets/099d8010-9f89-4536-b7b1-392d1667be3c" alt="Relay logo" />
</p>

<h1 align="center">Relay</h1>

<p align="center">
  Reliable webhook delivery infrastructure for modern applications.
</p>

<p align="center">
  <a href="https://github.com/rajputomsingh/relay/stargazers"><img src="https://img.shields.io/github/stars/rajputomsingh/relay?style=flat-square" alt="GitHub stars" /></a>
  <a href="https://github.com/rajputomsingh/relay/blob/main/LICENSE"><img src="https://img.shields.io/github/license/rajputomsingh/relay?style=flat-square" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go" />
  <img src="https://img.shields.io/badge/MongoDB-47A248?style=flat-square&logo=mongodb&logoColor=white" alt="MongoDB" />
</p>

> **Release status:** The latest published release is **v1.0.0 — Production Ready** ([tag](https://github.com/rajputomsingh/relay/releases/tag/v1.0.0)). The `main` branch tracks stable development.

Built with Go and MongoDB, Relay accepts events over HTTP, persists them in MongoDB, and delivers them asynchronously to matching webhook endpoints with retries, delivery tracking, and replay. Ingestion and delivery are separate stages so the ingest path stays fast and predictable while the delivery engine applies backoff, concurrency limits, and recovery independently.

- **Runtime:** Go
- **Persistence:** MongoDB 7 (Go Driver v2)
- **Delivery:** Bounded workers, retries with backoff, per-endpoint state
- **Operations:** Delivery history, health checks, graceful shutdown, Prometheus metrics
- **API:** Fully documented OpenAPI (Swagger) specification

## Quick Start

### Docker (Recommended)

The fastest way to get Relay running locally is with Docker Compose:

```bash
git clone https://github.com/rajputomsingh/relay.git
cd relay
make docker-up
```

_See `docs/deployment.md` for production configuration details._

### Manual Setup

```bash
git clone https://github.com/rajputomsingh/relay.git
cd relay
go mod download
```

Configure `.env` from `.env.example` (see [Configuration](#configuration)), then:

```bash
go run ./cmd/relay
```

Validate changes with the full gate:

```bash
go test ./...
go vet ./...
go build ./...
git diff --check
```

## Architecture

<p align="center">
  <img
    src="assets/architecture.png" 
    alt="Relay architecture"
    width="100%"
  />
</p>

Each accepted event flows through five stages:

1. **Ingestion.** `POST /v1/events` authenticates the caller, validates the JSON body, and enforces `Idempotency-Key`.
2. **Persistence.** Events are written to MongoDB before any delivery begins. Each matching endpoint gets an independent delivery record.
3. **Matching and scheduling.** Enabled endpoints are filtered by `source` and `event_type`. Matching deliveries enter the pending pool.
4. **Bounded delivery workers.** Workers claim deliveries with lease tokens, execute the outbound HTTP request, and record the attempt.
5. **Outcome and recovery.** Attempts resolve to success, retry with backoff, or terminal failure. Expired leases are reclaimed; terminal failures are eligible for replay.

## Delivery Semantics

Relay provides **at-least-once** delivery. It does **not** guarantee exactly-once processing — a successful HTTP response plus a persisted attempt record cannot jointly guarantee a single downstream effect. Any downstream requiring single-effect semantics must key idempotency on the event `id`.

- **Idempotency keys.** The `Idempotency-Key` header rejects duplicate submissions of the same logical event. It prevents duplicate ingestion; it does not prevent duplicate delivery attempts.
- **Retry classification.** Relay retries `5xx`, network errors, connection refusals, and timeouts. `4xx` other than `408` and `429` are terminal.
- **Worker crash.** If a worker dies mid-attempt, its lease expires and the delivery is reclaimed. The retry after reclamation is logged as a new attempt. Downstreams may see a delivery more than once.
- **Lease fencing.** A stale worker attempting to record an outcome after its lease is reclaimed is rejected, so stale results cannot overwrite current state.
- **Downstream idempotency.** Because duplicate delivery is possible, downstream handlers **must** be idempotent.

## Configuration

Relay is configured entirely through environment variables, read at startup; validation failures abort the process.

| Variable                       | Required | Default       | Notes                                    |
| ------------------------------ | -------- | ------------- | ---------------------------------------- |
| `APP_ENV`                      | No       | `development` | `development` / `staging` / `production` |
| `PORT`                         | No       | `8080`        | Valid TCP port                           |
| `MONGODB_URI`                  | Yes      | —             | Must parse as a MongoDB URI              |
| `MONGODB_DATABASE`             | No       | `relay`       | Non-empty                                |
| `RELAY_API_KEY`                | Yes      | —             | Validated for minimum length (32 chars)  |
| `RELAY_WEBHOOK_URL`            | No       | —             | First-run bootstrap only                 |
| `RELAY_WEBHOOK_SIGNING_SECRET` | No       | —             | If set, at least 32 characters           |

`.env` and files containing credentials must never be committed. In production, source these from a secrets manager.

## API Contract & Documentation

All `/v1/*` routes require `Authorization: Bearer <RELAY_API_KEY>`.

Relay includes an interactive Swagger UI. When running the server locally, navigate to:
👉 **http://localhost:8080/swagger/index.html**

| Method   | Endpoint                     | Auth | Purpose                           |
| -------- | ---------------------------- | ---- | --------------------------------- |
| `GET`    | `/healthz`                   | No   | Liveness (process running)        |
| `GET`    | `/readyz`                    | No   | Readiness (MongoDB reachable)     |
| `GET`    | `/metrics`                   | Yes  | Prometheus metrics endpoint       |
| `POST`   | `/v1/events`                 | Yes  | Submit an event                   |
| `POST`   | `/v1/events/{id}/replay`     | Yes  | Replay an eligible failed event   |
| `POST`   | `/v1/endpoints`              | Yes  | Register an endpoint              |
| `GET`    | `/v1/endpoints`              | Yes  | List endpoints                    |
| `GET`    | `/v1/endpoints/{id}`         | Yes  | Retrieve one endpoint             |
| `PATCH`  | `/v1/endpoints/{id}`         | Yes  | Update endpoint configuration     |
| `DELETE` | `/v1/endpoints/{id}`         | Yes  | Disable an endpoint (soft delete) |
| `GET`    | `/v1/events/{id}/deliveries` | Yes  | Per-endpoint delivery status      |
| `GET`    | `/v1/events/{id}/attempts`   | Yes  | Attempt history                   |

## Webhook Signing

When an endpoint has a signing secret, Relay signs outbound requests with `HMAC-SHA256` over the raw body and sends the hex digest in `X-Relay-Signature`.

**Verify in Go**

```go
func verifyRelaySignature(rawBody []byte, header string, secret []byte) error {
    if header == "" {
        return errors.New("missing X-Relay-Signature")
    }
    mac := hmac.New(sha256.New, secret)
    mac.Write(rawBody)
    expected := hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(header), []byte(expected)) {
        return errors.New("signature mismatch")
    }
    return nil
}
```

**Verify in Node.js**

```js
function verifyRelaySignature(rawBody, header, secret) {
  if (!header) throw new Error("missing X-Relay-Signature");
  const expected = crypto
    .createHmac("sha256", secret)
    .update(rawBody)
    .digest("hex");
  const a = Buffer.from(header, "utf8");
  const b = Buffer.from(expected, "utf8");
  if (a.length !== b.length || !crypto.timingSafeEqual(a, b)) {
    throw new Error("signature mismatch");
  }
}
```

## Operations & Observability

- **Health.** `/healthz` returns `200` while the process runs. `/readyz` returns `200` only when MongoDB responds to ping.
- **Metrics.** `/metrics` exposes Prometheus metrics for outbound delivery duration and delivery attempt outcomes (success/failure).
- **Shutdown.** On `SIGINT`/`SIGTERM`, Relay stops accepting new requests, drains in-flight deliveries up to the shutdown deadline, then exits.
- **Lease recovery.** Pending deliveries hold a lease token and expiry. If a worker fails, the lease expires and the delivery is reclaimed.
- **Retry policy.** Backoff is `retryDelay(n) = 2s × 2^(n-1) + jitter(0–25%)`, capped per delay and bounded by `max_attempts`.

## Benchmarks

Relay includes a benchmark harness for sustained load, concurrency, failure modes, outage recovery, and queue growth. Full methodology and raw results: [docs/benchmarks.md](docs/benchmarks.md).

All numbers are from a single Windows host with Go 1.27.0 and MongoDB 7 in Docker.

| Measurement                 | Result            |
| --------------------------- | ----------------- |
| Workload                    | 5,000 deliveries  |
| Highest tested concurrency  | 32                |
| Highest reported throughput | 43.5 deliveries/s |
| Highest-concurrency drain   | 115 s             |

## Known Limitations

- Metrics currently query MongoDB on scrape. For very high-throughput setups, an in-memory metrics aggregator is recommended.
- No tenant isolation or per-tenant authorization (single-tenant instance per deployment).
- No web UI for event inspection, monitoring, or replay (API-only).
- End-to-end tests spanning the full network stack are not yet automated.

## Project Status and Roadmap

**Completed (v1.0.0)**

- Production-ready Dockerfile & Compose setup.
- Prometheus metrics instrumentation.
- OpenAPI (Swagger) interactive documentation.
- Hardened CI/CD pipeline (CircleCI).

**Planned (v1.1.0+)**

- First-class metrics and alerting dashboards (Grafana).
- Tenant isolation and multi-tenant authorization.
- Web dashboard for event inspection and replay.

## Development and Testing

**Unit tests (no infrastructure):**

```bash
go test ./internal/config ./internal/security ./internal/handlers -count=1
```

**Integration tests (MongoDB required):**

```bash
export MONGODB_TEST_URI="mongodb://localhost:27017"
go test ./internal/delivery -count=1
```

**Full validation gate (before proposing changes):**

```bash
go test ./...
go vet ./...
go build ./...
git diff --check
```

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.
