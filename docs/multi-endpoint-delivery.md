# Multi-Endpoint Delivery

Relay delivers each accepted event to every enabled endpoint whose event filter matches. Delivery state, retry policy, attempt history, and signing secrets are managed per endpoint.

## Data Model

### `endpoints` collection

| Field | Type | Description |
|---|---|---|
| `_id` | ObjectID | Endpoint identifier |
| `name` | string | Human-readable endpoint name |
| `url` | string | Destination URL |
| `signing_secret` | string | Optional HMAC-SHA256 signing secret |
| `enabled` | boolean | Whether the endpoint can receive deliveries |
| `event_filter` | object | Optional source and event-type filters |
| `max_attempts` | integer | Maximum attempts, from 1 to 20 |
| `created_at` | timestamp | Creation time |
| `updated_at` | timestamp | Last update time |
| `disabled_at` | timestamp | Time the endpoint was disabled, when applicable |

Endpoint names are subject to the uniqueness constraints implemented by the endpoint store and database indexes.

### `deliveries` collection

Each document represents one event-to-endpoint delivery.

| Field | Type | Description |
|---|---|---|
| `_id` | ObjectID | Delivery identifier |
| `event_id` | ObjectID | Reference to `events._id` |
| `endpoint_id` | ObjectID | Reference to `endpoints._id` |
| `status` | string | `pending`, `processing`, `delivered`, or `failed` |
| `attempts` | integer | Number of attempts consumed |
| `max_attempts` | integer | Retry limit captured when the delivery is created |
| `next_attempt_at` | timestamp | Earliest time the delivery can be retried |
| `lease_until` | timestamp | Expiration of the active processing lease |
| `lease_token` | string | Token used to fence delivery state updates |
| `delivered_at` | timestamp | Successful delivery time |
| `failed_at` | timestamp | Terminal failure time |
| `last_error` | string | Most recent failure reason |
| `created_at` | timestamp | Creation time |
| `updated_at` | timestamp | Last update time |

A unique compound index on `(event_id, endpoint_id)` prevents duplicate delivery records for the same event and endpoint.

### `delivery_attempts` collection

Each attempt records an individual delivery operation.

| Field | Type | Description |
|---|---|---|
| `_id` | ObjectID | Attempt identifier |
| `event_id` | ObjectID | Associated event |
| `endpoint_id` | ObjectID | Associated endpoint |
| `attempt_number` | integer | Attempt sequence number |
| `started_at` | timestamp | Attempt start time |
| `finished_at` | timestamp | Attempt completion time |
| `duration_ms` | integer | Attempt duration in milliseconds |
| `http_status` | integer | HTTP response status, when available |
| `outcome` | string | Attempt result |
| `error` | string | Failure details, when applicable |

Every newly recorded attempt must include both `event_id` and `endpoint_id`.

## Delivery Lifecycle

1. **Event accepted:** Relay persists the event.
2. **Fan-out:** Relay finds enabled endpoints whose filters match the event.
3. **Delivery creation:** Relay creates one pending delivery per matching endpoint.
4. **Claim:** A worker atomically claims a due delivery and assigns a lease token.
5. **Dependency loading:** The worker loads the event and endpoint.
6. **Webhook request:** Relay sends the event payload to the endpoint.
7. **Outcome recording:** Relay records the attempt and updates the delivery state.
8. **Retry or completion:** Failed requests are retried according to the delivery's retry policy until success or exhaustion.

## Worker Algorithm

1. Recover expired processing leases that have exhausted their retry limit by marking those deliveries failed.
2. Atomically claim a due pending delivery or an eligible expired processing delivery.
3. Assign a new lease token and lease expiration.
4. Load the referenced event and endpoint.
5. If the event or endpoint is missing, or the endpoint is disabled, terminate the delivery without sending a webhook.
6. Serialize the event payload and sign it when the endpoint has a configured signing secret.
7. Send an HTTP POST using the configured request timeout.
8. Record the attempt with its event ID and endpoint ID.
9. On a 2xx response, mark the delivery delivered and clear its active lease.
10. On a retryable failure, schedule the next attempt with exponential backoff and jitter. Mark the delivery failed when its attempt limit is exhausted.

All delivery state changes must be fenced by the active lease token so that a stale worker cannot overwrite a newer claim's state.

## Retry Semantics

- The delivery's `max_attempts` value is captured when fan-out creates the delivery.
- Updating an endpoint's retry limit does not automatically change existing delivery records.
- Non-2xx HTTP responses and transport failures are retryable, subject to the configured attempt limit.
- Missing events, missing endpoints, and disabled endpoints are terminal failures under this design.
- Retry delays should use exponential backoff with jitter and a configured upper bound.
- Attempt counts and retry scheduling must be consistent across worker restarts and expired leases.

The precise backoff parameters must match the implementation before this document is treated as a verified operational specification.

## Per-Endpoint Signing

Each endpoint can have its own optional `signing_secret`.

- When configured, Relay signs each outgoing request with that endpoint's secret.
- When omitted, Relay sends the request without a signature.
- Each signed attempt uses a fresh timestamp.

The signing protocol is documented in [`webhook-signing.md`](webhook-signing.md). The signature uses the `v1=` prefix and HMAC-SHA256 over the timestamp, a period, and the exact request body.

The corresponding request headers are:

- `X-Relay-Timestamp`
- `X-Relay-Signature`

## Backward Compatibility

On startup, if the `endpoints` collection is empty and `RELAY_WEBHOOK_URL` is configured, Relay can bootstrap a default endpoint using the legacy webhook configuration.

The default endpoint can also inherit the configured signing secret and maximum delivery attempts.

After bootstrap, delivery routing and signing are managed through endpoint records. The legacy environment variables should not override endpoint-specific settings during normal delivery processing.

## API

### Endpoint management

| Method | Path | Description |
|---|---|---|
| POST | `/v1/endpoints` | Create an endpoint |
| GET | `/v1/endpoints` | List endpoints |
| GET | `/v1/endpoints/{id}` | Retrieve an endpoint |
| PATCH | `/v1/endpoints/{id}` | Partially update an endpoint |
| DELETE | `/v1/endpoints/{id}` | Disable an endpoint |

### Event delivery observability

| Method | Path | Description |
|---|---|---|
| GET | `/v1/events/{id}/deliveries` | Retrieve per-endpoint delivery state |
| GET | `/v1/events/{id}/attempts` | Retrieve attempt history, including endpoint IDs |

All endpoints require bearer-token authentication using `RELAY_API_KEY`, unless the application's authentication configuration explicitly specifies otherwise.

## Operational Verification

Before release, verify that:

- One event can produce deliveries for multiple matching endpoints.
- Each delivery retains independent status and retry state.
- Attempt records include both event and endpoint IDs.
- Endpoint-specific signing works with independent secrets.
- Retry exhaustion and expired lease recovery work correctly.
- Concurrent workers cannot overwrite newer delivery state.
- Both event observability endpoints return the expected data.
- Unit tests, integration tests, `go vet`, and `go build` pass.
