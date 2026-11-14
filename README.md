# Relay

<p align="center">
  <img src="https://github.com/user-attachments/assets/099d8010-9f89-4536-b7b1-392d1667be3c" width="110" height="110" alt="Relay logo" />
</p>

<p align="center">
  <strong>Relay</strong>
  <br />
  Reliable webhook delivery infrastructure, built with Go.
</p>

<p align="center">
  <a href="https://github.com/rajputomsingh/relay/actions"><img src="https://img.shields.io/badge/CI-GitHub_Actions-blue?logo=githubactions&logoColor=white" alt="CI" /></a>
  <a href="https://goreportcard.com/report/github.com/rajputomsingh/relay"><img src="https://goreportcard.com/badge/github.com/rajputomsingh/relay" alt="Go Report Card" /></a>
  <a href="https://github.com/rajputomsingh/relay/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue" alt="Apache 2.0 License" /></a>
  <a href="https://github.com/rajputomsingh/relay/issues"><img src="https://img.shields.io/github/issues/rajputomsingh/relay" alt="Issues" /></a>
  <a href="https://github.com/rajputomsingh/relay"><img src="https://img.shields.io/github/stars/rajputomsingh/relay" alt="GitHub Stars" /></a>
  <a href="https://github.com/rajputomsingh/relay/discussions"><img src="https://img.shields.io/github/discussions/rajputomsingh/relay" alt="Discussions" /></a>
</p>

<p align="center">
  <a href="https://github.com/rajputomsingh/relay/tree/main/sdk-go">
    <img src="https://img.shields.io/badge/Go_SDK-Available-00ADD8?logo=go&logoColor=white" alt="Go SDK" />
  </a>
  <a href="https://github.com/rajputomsingh/relay/tree/main/sdk-node">
    <img src="https://img.shields.io/badge/Node.js_SDK-Available-339933?logo=nodedotjs&logoColor=white" alt="Node.js SDK" />
  </a>
  <a href="https://github.com/rajputomsingh/relay/tree/main/sdk-python">
    <img src="https://img.shields.io/badge/Python_SDK-Available-3776AB?logo=python&logoColor=white" alt="Python SDK" />
  </a>
</p>

Relay is a self-hosted webhook delivery service written in Go, with MongoDB as its persistence layer. It accepts events over HTTP and delivers them asynchronously to registered endpoints.

Webhook delivery becomes difficult when receiving services go offline, requests time out, or workers stop unexpectedly. Applications often end up implementing their own retry logic, queues, and delivery tracking. Relay handles these parts separately so the application sending an event does not have to manage every delivery attempt.

## Features

Relay supports event ingestion, idempotency, concurrent delivery workers, automatic retries, delivery tracking, and event replay. It uses expiring leases and fencing to recover work left behind by failed workers. Outgoing webhooks can also be signed using HMAC-SHA256.

The project includes Prometheus metrics, Grafana dashboards, Docker deployment support, and official SDKs for Go, Node.js, and Python.

## Architecture

<p align="center">
  <img src="assets/architecture.png" width="100%" alt="Relay system architecture" />
</p>

Relay separates the API that accepts events from the workers responsible for delivering them. When an application submits an event, Relay validates the request, checks its idempotency key, and stores the event in MongoDB along with delivery records for matching endpoints.

Background workers claim pending deliveries and send HTTP requests. Each attempt is recorded, and unsuccessful deliveries are scheduled for retry when appropriate. If a worker crashes before completing its work, the lease eventually expires and another worker can reclaim the delivery.

## Getting Started

You'll need Go, MongoDB 7 or a compatible deployment, and optionally Docker with Docker Compose.

Clone the repository and move into the project directory:

```bash
git clone https://github.com/rajputomsingh/relay.git
cd relay
```

Create your environment file:

```bash
cp .env.example .env
```

Configure MongoDB and the API credentials:

```env
APP_ENV=development
PORT=8080
MONGODB_URI=mongodb://localhost:27017
MONGODB_DATABASE=relay
RELAY_API_KEY=your-secure-api-key
```

Start Relay using Docker Compose:

```bash
make docker-up
```

Alternatively, run it directly:

```bash
go mod download
go run ./cmd/relay
```

Once the server is running, check its health and database connection:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8080/v1/health
```

If the containers fail to start or Relay cannot reach MongoDB, see the [Docker Compose troubleshooting guide](docs/deployment.md#troubleshooting).

## Sending Events

Relay exposes an HTTP API for submitting events. Requests to `/v1/*` endpoints require Bearer token authentication.

```bash
curl -X POST http://localhost:8080/v1/events \
  -H "Authorization: Bearer YOUR_RELAY_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: user-created-001" \
  -d '{
    "source": "my-app",
    "event_type": "user.created",
    "data": {
      "userId": 123
    }
  }'
```

The idempotency key identifies the submission and prevents the same event from being ingested repeatedly.

## SDKs

Relay has official SDKs for Go, Node.js/TypeScript, and Python. Each client provides an interface for submitting events without having to construct HTTP requests manually.

**Go**

```bash
go get github.com/rajputomsingh/relay-go
```

```go
client := relay.NewClient("your-api-key")

resp, err := client.SendEvent(
    context.Background(),
    relay.Event{
        Source:    "my-app",
        EventType: "user.created",
        Data:      map[string]interface{}{"userId": 123},
    },
    "user-created-001",
)
```

**Node.js / TypeScript**

```bash
npm install @rajputomsingh/relay-node
```

```typescript
import { Relay } from "@rajputomsingh/relay-node";

const relay = new Relay({
  apiKey: process.env.RELAY_API_KEY!,
});

await relay.sendEvent(
  {
    source: "my-app",
    event_type: "user.created",
    data: { userId: 123 },
  },
  "user-created-001",
);
```

**Python**

```bash
pip install relay-python-sdk
```

```python
from relay_python import Relay

client = Relay(
    api_key="your-api-key",
    base_url="http://localhost:8080",
)

client.send_event(
    source="my-app",
    event_type="user.created",
    data={"userId": 123},
    idempotency_key="user-created-001",
)
```

## Delivery and Retries

Relay uses at-least-once delivery semantics. A delivery can be attempted more than once, so receiving applications should handle duplicate events using a stable event identifier.

HTTP 2xx responses are treated as successful deliveries. HTTP 408, 429, and 5xx responses, along with network and connection errors, are eligible for retry. Other HTTP 4xx responses are treated as terminal failures.

Retry delays use exponential backoff with jitter and are limited by the configured maximum number of attempts. Deliveries that reach the attempt limit can be replayed through the API.

Workers use lease tokens to coordinate ownership of deliveries. Expired leases allow unfinished work to be reclaimed, while lease fencing prevents an outdated worker from overwriting a newer result.

## API Reference

The API covers event submission, endpoint management, delivery inspection, and event replay. Health and monitoring endpoints are also available.

Interactive API documentation is available through Swagger UI when Relay is running:

```text
http://localhost:8080/swagger/index.html
```

The API specification is maintained in the repository.

## Security

Relay supports HMAC-SHA256 signatures for outgoing webhook requests. When an endpoint has a signing secret configured, Relay signs the original request body and includes the signature in the `X-Relay-Signature` header.

Receiving applications should verify signatures against the raw request body using constant-time comparison. API keys and signing secrets should be kept out of source control, and production deployments should use HTTPS.

## Monitoring

Relay exposes Prometheus metrics through its authenticated `/metrics` endpoint and includes a Grafana dashboard for monitoring delivery activity.

The monitoring stack can be started separately:

```bash
docker compose -f docker-compose.monitoring.yml up -d
```

Health checks, delivery attempt history, graceful shutdown, and lease recovery are also included.

### Service health

`GET /v1/health` reports the health of the Relay service itself and needs no authentication, so load balancers, orchestrators, and uptime monitors can poll it:

```json
{
  "status": "ok",
  "mongo": "ok",
  "worker": "ok",
  "version": "dev",
  "uptime_seconds": 3612
}
```

| Field | Values | Meaning |
| --- | --- | --- |
| `status` | `ok`, `degraded`, `down` | `down` when MongoDB is unreachable, `degraded` when the worker is not `ok`, otherwise `ok` |
| `mongo` | `ok`, `unreachable` | MongoDB answered a ping within 2 seconds |
| `worker` | `ok`, `stale`, `disabled` | `stale` when the delivery worker has not polled for more than three poll intervals, `disabled` when it never started; omitted when MongoDB is unreachable |
| `version` | string | build version, `dev` unless set with `-ldflags "-X main.version=<version>"` |
| `uptime_seconds` | integer | seconds since the process started |

The endpoint returns `200` for `ok` and `degraded`, and `503` for `down`. An idle worker that finds nothing to deliver still reports `ok`, because worker health comes from its last poll rather than its last delivery. For per-destination health, use `GET /v1/endpoints/{id}/health`.

## Benchmarks

The repository includes a benchmark harness for testing delivery throughput, worker concurrency, failure scenarios, and recovery.

One recorded local run processed 5,000 deliveries using up to 32 workers, with a reported peak throughput of 43.5 deliveries per second. The highest-concurrency run completed in 115 seconds.

These results were recorded on a single Windows development machine running Go 1.27.0 and MongoDB 7 in Docker. They are specific to that environment.

The benchmark methodology and results are available in [docs/benchmarks.md](docs/benchmarks.md).

## Development

Install dependencies:

```bash
go mod download
```

Run the application:

```bash
go run ./cmd/relay
```

Run the unit tests:

```bash
go test ./internal/config ./internal/security ./internal/handlers -count=1
```

Integration tests require MongoDB and the `MONGODB_TEST_URI` environment variable:

```bash
export MONGODB_TEST_URI="mongodb://localhost:27017"
go test ./internal/delivery -count=1
```

Before submitting changes, run the complete validation suite:

```bash
go test ./...
go vet ./...
go build ./...
git diff --check
```

## Current Limitations

Relay currently targets single-tenant deployments. Tenant isolation and per-tenant authorization are not implemented. There is no dedicated web dashboard for managing events and endpoints, and metrics collection currently queries MongoDB during Prometheus scrapes. Full network-level end-to-end testing is also an area for further work.

## Roadmap

Upcoming work includes tenant isolation, a web dashboard for event inspection and replay, expanded end-to-end testing, and further improvements to performance, observability, and webhook security.

## Contributing

Relay is open source, and contributions are welcome. If you're interested in Go, backend infrastructure, distributed systems, or webhook delivery, take a look at the open issues.

For substantial changes, please open an issue first so the proposed approach can be discussed before implementation. Bug fixes, tests, documentation improvements, and focused pull requests are welcome.

[Issues](https://github.com/rajputomsingh/relay/issues) · [Good First Issues](https://github.com/rajputomsingh/relay/labels/good%20first%20issue) · [Contributing Guidelines](CONTRIBUTING.md)

## Contributors

Relay is built with contributions from the community.

<!-- contributors:start -->
<a href="https://github.com/rajputomsingh"><img src="https://avatars.githubusercontent.com/u/175422536?v=4&s=48" width="48" height="48" style="border-radius: 50%;" alt="rajputomsingh"></a> <a href="https://github.com/sospeter-57"><img src="https://avatars.githubusercontent.com/u/150504913?v=4&s=48" width="48" height="48" style="border-radius: 50%;" alt="sospeter-57"></a> <a href="https://github.com/IG-ABHINAV"><img src="https://avatars.githubusercontent.com/u/127868165?v=4&s=48" width="48" height="48" style="border-radius: 50%;" alt="IG-ABHINAV"></a> <a href="https://github.com/sinbumu"><img src="https://avatars.githubusercontent.com/u/21115111?v=4&s=48" width="48" height="48" style="border-radius: 50%;" alt="sinbumu"></a> <a href="https://github.com/sharanyashetty013-droid"><img src="https://avatars.githubusercontent.com/u/220977966?v=4&s=48" width="48" height="48" style="border-radius: 50%;" alt="sharanyashetty013-droid"></a> 
<!-- contributors:end -->

<p align="center">
  <sub>
    View the
    <a href="https://github.com/rajputomsingh/relay/graphs/contributors">GitHub contributors graph</a>
    or the
    <a href="CONTRIBUTORS.md">project contributor list</a>.
  </sub>
</p>

## License

Relay is licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

---

<p align="center">
  <strong>Relay</strong>
  <br />
  Built with Go.
</p>
