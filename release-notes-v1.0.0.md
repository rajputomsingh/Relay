# Relay v1.0.0 - Production Ready

## Highlights

- **Production Deployment:** Added a multi-stage `Dockerfile` (distroless, non-root) and a `docker-compose.yml` for one-command local and production setups.
- **Observability:** Added a Prometheus `/metrics` endpoint tracking delivery outcomes, latency, and worker health.
- **API Contract:** Added a full OpenAPI (Swagger) specification, available interactively at `/swagger/index.html`.
- **CI/CD Hardening:** Migrated to a fully functioning CircleCI pipeline with automated tests, builds, and Docker image generation.

## Core Platform

Relay is a webhook delivery service built with Go and MongoDB. It accepts authenticated events over HTTP, persists event and delivery state, and processes matching deliveries asynchronously.

Idempotency, retry handling, delivery inspection, and replay workflows help applications manage downstream delivery failures.

## Validation

The following checks are used to validate the project:

```bash
go test ./...
go vet ./...
go build ./...
```
