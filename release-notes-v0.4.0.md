# Relay v0.2.0 — Event Inspection API

## Highlights
- Added authenticated event inspection with GET /v1/events/{id}.
- Added delivery attempt history with cursor-based pagination.
- Added API key authentication and input validation.
- Excluded internal delivery errors from public responses.
- Added tests for authorization, validation, missing events, storage errors, and pagination.

## Validation
- go test ./...
- go vet ./...
- go build ./...

## Core Platform
Relay includes authenticated event ingestion, reliable webhook delivery, and delivery attempt history.
