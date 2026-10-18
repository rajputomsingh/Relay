# Relay v0.4.0 - Multi-Endpoint Delivery

## Highlights

- **Multi-endpoint delivery:** Deliver events to multiple registered endpoints, with independent delivery state for each endpoint.
- **Endpoint management:** Configure endpoints and route events using supported event filters.
- **Webhook signing:** Support outbound HMAC-SHA256 signatures for request authenticity.
- **Delivery inspection:** Inspect event delivery status and individual delivery attempts.
- **Retry and recovery:** Handle transient delivery failures through retries and worker recovery.
- **Dead-letter handling:** Support inspection and replay workflows for eligible failed deliveries.
- **Observability:** Improve visibility into delivery attempts, worker health, and operational state.

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

## Configuration and Security

- Configure environment variables using `.env.example`.
- Keep API keys, MongoDB credentials, and webhook signing secrets out of source control.
- Configure endpoint destinations carefully and use HTTPS in production.
- Verify endpoint configuration and signing behavior in a staging environment before deployment.

## Compatibility and Release Scope

This document describes **v0.4.0 - Multi-Endpoint Delivery**.

The `main` branch contains subsequent development, including per-endpoint rate limiting and the v0.9.0 load, capacity, and recovery validation milestone. These later changes are not automatically included in the v0.4.0 release artifact.

For reproducible deployments, use the source associated with the intended release tag.

## Upgrade Notes

1. Review `.env.example` for the configuration expected by the version being deployed.
2. Configure MongoDB connectivity and the required API authentication settings.
3. Review endpoint destinations, event filters, and webhook signing configuration.
4. Run the validation commands against the release source.
5. Test delivery, retry, and recovery behavior in staging before production deployment.