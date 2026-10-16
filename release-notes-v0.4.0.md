    # Relay v0.4.0 - Multi-Endpoint Delivery

    ## Highlights

    - Introduced multi-endpoint webhook delivery, allowing accepted events to be distributed to multiple configured destinations.
    - Added endpoint management APIs for registering, listing, updating, and disabling webhook destinations.
    - Implemented event filtering to route events to matching, enabled endpoints.
    - Added independent delivery state, retry limits, and attempt history for each endpoint.
    - Introduced per-endpoint HMAC-SHA256 webhook signing.
    - Added persistent delivery tracking with lease-based concurrency protection.
    - Preserved backward compatibility through automatic default-endpoint initialization.

    ## Reliability & Security

    - Exponential backoff with jitter and bounded delivery attempts.
    - Atomic delivery claims and lease-token fencing to prevent stale workers from overwriting delivery state.
    - Endpoint-specific signing secrets for authenticating outbound webhook requests.
    - Persistent attempt history for debugging and delivery inspection.
    - Idempotent delivery creation using unique event-to-endpoint records.

    ## Validation

    Run the following commands to validate the project:

    ```bash
    go test ./...
    go vet ./...
    go build ./...
    ```

    ## Core Platform

    Relay is a webhook delivery engine built with Go and MongoDB. It provides authenticated event ingestion, idempotent persistence, asynchronous delivery, automatic retries, multi-endpoint fan-out, delivery inspection, and event replay.

    For implementation details, see [Multi-Endpoint Delivery](docs/multi-endpoint-delivery.md).

    ---

    **Release:** v0.4.0  
    **Focus:** Multi-endpoint delivery infrastructure
