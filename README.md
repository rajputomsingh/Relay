<p align="center">
  <img width="120" height="120" src="https://github.com/user-attachments/assets/099d8010-9f89-4536-b7b1-392d1667be3c" alt="Relay logo" />
</p>

<h1 align="center">Relay</h1>
<p align="center">
  Reliable webhook delivery for modern applications.
  <br />
  Built with Go and MongoDB to handle event ingestion, retries, delivery tracking, and replay.
</p>
<p align="center">
  <a href="https://github.com/rajputomsingh/relay/stargazers"><img src="https://img.shields.io/github/stars/rajputomsingh/relay?style=flat-square" alt="GitHub stars" /></a>
  <a href="https://github.com/rajputomsingh/relay/blob/main/LICENSE"><img src="https://img.shields.io/github/license/rajputomsingh/relay?style=flat-square" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go" />
  <img src="https://img.shields.io/badge/MongoDB-47A248?style=flat-square&logo=mongodb&logoColor=white" alt="MongoDB" />
</p>

## Overview

Webhook delivery is a distributed systems problem. Network failures, unavailable endpoints, duplicate requests, and ambiguous delivery outcomes make reliable event propagation difficult to implement independently in every application.

Relay centralizes this responsibility. Applications submit events to Relay, which persists accepted events and manages downstream delivery, retry scheduling, failure tracking, and replay.

The system is designed to separate event ingestion from delivery execution while maintaining persistent records of event state and delivery attempts.

### Architecture

<p align="center">
  <img
    src="https://github.com/user-attachments/assets/ca51cc58-2de8-45fe-9db4-5cf47496068e"
    alt="Relay system architecture illustrating event ingestion, delivery workers, MongoDB persistence, webhook endpoints, and observability"
    width="100%"
  />
</p>

### Core Capabilities

- **Ingestion:** Authenticated JSON event ingestion over HTTP.
- **Idempotency:** Duplicate-submission protection through idempotency keys.
- **Persistence:** MongoDB storage for events and delivery-attempt history.
- **Delivery:** Asynchronous webhook delivery through Go workers.
- **Retries:** Exponential backoff with jitter and bounded attempts.
- **Observability:** Persistent attempt outcomes, durations, HTTP statuses, and errors.
- **Replay:** Manual replay of eligible failed events.
- **Operations:** Liveness, database readiness, and graceful shutdown.

### Delivery Lifecycle

Relay validates and persists incoming events before processing downstream delivery. Each attempt updates delivery state and records its outcome. Failed deliveries are retried according to the configured policy; eligible failed events can be replayed manually.

<p align="center">
  <img
    src="https://github.com/user-attachments/assets/50e6e38c-85e2-4bde-96ef-d2574647ecb6"
    alt="Webhook Event Delivery Flowchart"
    width="100%"
  />
</p>

## Technology Stack

| Component | Technology |
|---|---|
| Language | <img src="https://cdn.simpleicons.org/go/00ADD8" width="18" height="18" alt="Go" /> Go |
| HTTP API | <img src="https://cdn.simpleicons.org/openapiinitiative/6BA539" width="18" height="18" alt="HTTP API" /> HTTP and JSON |
| Persistence | <img src="https://cdn.simpleicons.org/mongodb/47A248" width="18" height="18" alt="MongoDB" /> MongoDB |
| Database Driver | <img src="https://cdn.simpleicons.org/mongodb/47A248" width="18" height="18" alt="MongoDB" /> MongoDB Go Driver v2 |
| Configuration | <img src="https://cdn.simpleicons.org/dotenv/ECD53F" width="18" height="18" alt="Environment variables" /> Environment variables |

## Project Structure

```text
relay/
├── cmd/
│   └── relay/
│       └── main.go
├── internal/
│   ├── config/
│   ├── database/
│   ├── delivery/
│   └── handlers/
├── .env.example
├── CONTRIBUTING.md
├── LICENSE
├── go.mod
└── README.md
```

## Getting Started

### Prerequisites

- Go version compatible with `go.mod`
- MongoDB Atlas or a reachable MongoDB instance
- Git

### Installation

```bash
git clone https://github.com/rajputomsingh/relay.git
cd relay
go mod download
```

### Configuration

Copy `.env.example` to `.env`.

PowerShell:

```powershell
Copy-Item .env.example .env
```

Bash:

```bash
cp .env.example .env
```

Configure the required environment variables:

```dotenv
APP_ENV=development
PORT=8080
MONGODB_URI=your_mongodb_connection_string
MONGODB_DATABASE=relay
RELAY_API_KEY=your_secret_api_key
```

Use values compatible with the application's configuration. Never commit `.env` or expose credentials.

### Run

```bash
go run ./cmd/relay
```

### Verify

```bash
go test ./...
go build ./...
```

## API

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/healthz` | Service liveness |
| `GET` | `/readyz` | Database readiness |
| `POST` | `/v1/events` | Submit an authenticated event |
| `POST` | `/v1/events/{id}/replay` | Replay an eligible failed event |

### Submit an event

Request:

```http
POST /v1/events
Authorization: Bearer YOUR_RELAY_API_KEY
Content-Type: application/json
Idempotency-Key: payment-1001
```

Body:

```json
{
  "source": "payments-service",
  "event_type": "payment.completed",
  "data": {
    "order_id": "order-1001",
    "amount": 1499,
    "currency": "INR"
  }
}
```

### PowerShell example

```powershell
$headers = @{
    Authorization = "Bearer $env:RELAY_API_KEY"
    "Idempotency-Key" = "relay-test-001"
}

$body = @{
    source = "local-test"
    event_type = "relay.test"
    data = @{
        message = "Hello from Relay"
    }
} | ConvertTo-Json -Depth 5

Invoke-RestMethod `
    -Uri "http://localhost:8080/v1/events" `
    -Method Post `
    -Headers $headers `
    -ContentType "application/json" `
    -Body $body
```

Set `RELAY_API_KEY` in the local environment before running the request.

## Delivery Attempt History

Relay stores delivery-attempt records in the MongoDB `delivery_attempts` collection.

| Field | Purpose |
|---|---|
| `event_id` | Associated event |
| `attempt_number` | Attempt sequence number |
| `started_at` | Attempt start time |
| `finished_at` | Attempt completion time |
| `duration_ms` | Attempt duration |
| `http_status` | HTTP response status, when available |
| `outcome` | Delivery outcome |
| `error` | Failure details, when applicable |

An index on `event_id` and descending `started_at` supports retrieving recent attempts for an event.

Attempt-history persistence failures are logged separately so that a history-write failure does not replace the primary delivery result.

## Reliability and Security

Relay incorporates bounded retries, persistent event state, idempotency-key enforcement, delivery history, and controlled replay.

Deployments should additionally enforce:

- HTTPS for externally accessible endpoints.
- Strong API keys and credential rotation.
- Restricted MongoDB network and database access.
- Redaction of secrets and sensitive payloads from logs.
- Idempotent downstream event handlers.
- Monitoring and alerting for repeated delivery failures.

API-key authentication alone does not constitute a complete production security model. Deployment-specific access controls and operational safeguards remain necessary.

## Roadmap

- [ ] Expand automated integration and failure-path testing.
- [ ] Add a delivery-attempt query API.
- [ ] Introduce structured metrics and alerting.
- [ ] Implement endpoint registration and management.
- [ ] Add dead-letter handling for exhausted deliveries.
- [ ] Introduce tenant isolation and per-tenant access controls.
- [ ] Develop a dashboard for event inspection and replay.
- [ ] Document production deployment and operations.

## Contributing

Contributions and bug reports are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting a pull request.

Run the test suite and build before proposing changes:

```bash
go test ./...
go build ./...
```

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

---

Relay is an open-source foundation for reliable webhook delivery and event processing.

## Webhook Signing

Relay can sign outbound webhook requests with HMAC-SHA256. Set:

```dotenv
RELAY_WEBHOOK_SIGNING_SECRET=<at-least-32-characters>
