# Deployment Guide

This guide covers running Relay in a production environment using Docker and Docker Compose.

## Prerequisites

- Docker Engine v24.0+
- Docker Compose v2.20+
- A reachable MongoDB 7 instance (or use the included Compose service)

## Configuration

Relay reads its configuration entirely from environment variables. In production, these should be injected via your orchestrator (Kubernetes Secrets, AWS Secrets Manager, etc.) or an `.env` file passed to Docker Compose.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ENV` | No | `development` | Set to `production` to enable production safeguards (e.g., rejecting HTTP webhook URLs). |
| `PORT` | No | `8080` | The port the HTTP server listens on. |
| `MONGODB_URI` | **Yes** | - | Connection string for MongoDB. |
| `MONGODB_DATABASE` | No | `relay` | The database name to use. |
| `RELAY_API_KEY` | **Yes** | - | The bearer token required to access the API. |
| `RELAY_WORKER_POLL_SECONDS` | No | `2` | How often workers poll MongoDB for pending deliveries. |
| `RELAY_MAX_DELIVERY_ATTEMPTS` | No | `8` | Maximum retry attempts for a failed delivery. |
| `RELAY_WORKER_CONCURRENCY` | No | `4` | Number of parallel delivery workers (1-32). |
| `RELAY_WEBHOOK_SIGNING_SECRET` | No | - | If set, signs outbound webhooks with HMAC-SHA256 (min 32 chars). |

## Running with Docker Compose

The fastest way to deploy is using the provided `docker-compose.yml`. This starts both Relay and a MongoDB 7 container.

1. Create a `.env` file in the root directory:
```bash
RELAY_API_KEY=your-secure-production-key
```

2. Build and start the services:
```bash
make docker-up
# Or: docker compose up -d --build
```

3. Verify the service is healthy:
```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

## Security Hardening

The provided Dockerfile uses a **distroless** base image. This image contains no shell and no package manager, drastically reducing the attack surface. It runs as the non-root user `nonroot`.

Ensure that:
1. `RELAY_API_KEY` is generated with sufficient entropy (e.g., `openssl rand -hex 32`).
2. MongoDB is not exposed to the public internet.
3. `RELAY_WEBHOOK_SIGNING_SECRET` is stored securely and rotated periodically.

## Operations

- **Liveness:** Use `GET /healthz`. If this fails, restart the container.
- **Readiness:** Use `GET /readyz`. If this fails, Relay cannot connect to MongoDB and should not receive traffic.
- **Logs:** Relay outputs structured JSON logs to stdout. Use `docker logs relay-app -f` or your log aggregator to view them.
