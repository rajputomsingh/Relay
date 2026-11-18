# API error reference

This page lists every error response that the Relay HTTP API returns today, taken from the handlers in `internal/handlers/` and the validation in `internal/endpoints/`. The machine-readable counterpart is the OpenAPI (Swagger 2.0) specification in [`swagger.yaml`](swagger.yaml) / [`swagger.json`](swagger.json), also served at `/swagger/`.

## How errors are reported

Relay does not use numeric or symbolic error codes. A failed request is identified by its **HTTP status code** and a human-readable **`error` message** in a JSON body:

```json
{ "error": "invalid event ID" }
```

- Handle failures by status code first. Treat the `error` string as diagnostic text: it is not a versioned contract, so do not parse it for control flow beyond the cases listed here.
- Messages are listed verbatim below. Rows marked "varies" return a message that depends on the offending field (see the validation tables).
- Error bodies are `Content-Type: application/json`, with the exceptions listed under [Responses that are not `{"error": ...}`](#responses-that-are-not-error-).
- Retry guidance in this document is a recommendation derived from what the handler does; the server does not send `Retry-After` headers.

### Authentication

Every `/v1/*` route and `/metrics` requires `Authorization: Bearer <RELAY_API_KEY>`. `/healthz`, `/readyz`, `/v1/health` and `/swagger/` do not check the key.

## Error index

| Status | Meaning | Retry? |
| --- | --- | --- |
| 400 | The request is malformed or fails validation. | No. Fix the request. |
| 401 | The API key is missing or wrong. | No. Fix the credential. |
| 404 | The resource (event or endpoint) does not exist. | No. |
| 405 | The HTTP method is not allowed on this route. | No. |
| 409 | The request conflicts with current state. | Depends; see the table below. |
| 413 | The event body is larger than 1 MiB. | No. Send a smaller body. |
| 500 | The server failed while processing the request. | Usually yes; see per-route notes. |
| 503 | A dependency (MongoDB) or the delivery subsystem is unavailable. | Yes, with backoff, except "webhook delivery is disabled". |

## Errors by route

### `POST /v1/events` (submit an event)

| Status | `error` | Cause | Client handling |
| --- | --- | --- | --- |
| 400 | `Idempotency-Key must contain 1 to 128 characters` | The `Idempotency-Key` header is empty (after trimming) or longer than 128 characters. | Send a key of 1 to 128 characters. Do not retry unchanged. |
| 400 | `invalid JSON event payload` | The body is not valid JSON, has unknown fields, or the read failed (for example the body exceeded 1 MiB without a `Content-Length`). | Fix the payload. |
| 400 | `request must contain exactly one JSON object` | Data follows the first JSON object in the body. | Send a single object. |
| 400 | `source is required and must be at most 200 characters` | `source` is empty after trimming or longer than 200 characters. | Fix `source`. |
| 400 | `event_type is required and must be at most 200 characters` | `event_type` is empty after trimming or longer than 200 characters. | Fix `event_type`. |
| 400 | `data must be a JSON object` | `data` is missing or `null`. | Provide `data`. |
| 401 | `invalid or missing API key` | No `Bearer` token, or the token does not match. | Fix the credential. |
| 405 | `method not allowed` | A method other than `POST` reached this route. | Use `POST`. |
| 413 | `request body exceeds 1 MiB` | The declared `Content-Length` is larger than 1 MiB (1048576 bytes). | Send a smaller event. |
| 500 | `failed to persist event` | The event could not be stored. | Retry with the **same** `Idempotency-Key`; a duplicate key is answered with `200` and `"status": "already_accepted"`, so retries cannot create duplicates. |
| 500 | `event accepted but fan-out failed` | The event was stored but creating its deliveries failed. The body also contains `event_id`. | Do not resubmit as a new event. The event exists; a retry with the same key returns `already_accepted`. Use `GET /v1/events/{id}` and `GET /v1/events/{id}/deliveries` to check its state. The background reconciler re-runs fan-out for incomplete events. |

### `POST /v1/events/{id}/replay` (replay a failed event)

| Status | `error` | Cause | Client handling |
| --- | --- | --- | --- |
| 400 | `invalid event ID` | `{id}` is not a valid 24-character hex ObjectID. | Fix the ID. |
| 401 | `invalid or missing API key` | As above. | Fix the credential. |
| 405 | `method not allowed` | A method other than `POST` was used on a `/v1/events/...` path that is not `GET .../{id}`, `.../attempts` or `.../deliveries`. | Use `POST`. |
| 409 | `event not found, delivery status is not failed, or replay already in progress` | No event with this ID has `delivery_status` `failed` and a free replay claim. The handler returns this one message for all three cases. | Check the event with `GET /v1/events/{id}`. If its status is `failed` and another replay is running, wait (a claim expires after 30 seconds) and retry; otherwise do not retry. |
| 409 | `event has no failed deliveries to replay` | The event is `failed` but none of its deliveries are in `failed` state. | Do not retry; inspect `GET /v1/events/{id}/deliveries`. |
| 500 | `failed to claim replay` | The claim update failed in the database. | Retry with backoff. |
| 500 | `failed to requeue deliveries` | Requeuing the failed deliveries failed; the replay claim is released. | Retry with backoff. |
| 503 | `webhook delivery is disabled` | The server was started without delivery components. | Not retryable until the operator enables delivery. |

A request whose path does not end in `/replay` (or has extra path segments before it) returns a plain-text `404 page not found` after the API key check.

### `GET /v1/events` (list events)

| Status | `error` | Cause | Client handling |
| --- | --- | --- | --- |
| 400 | `delivery_status must be specified once` | The `delivery_status` parameter was repeated. | Send it once. |
| 400 | `invalid delivery_status` | Not one of `pending`, `processing`, `delivered`, `failed`, `disabled`, `no_destination`. | Use a valid value. |
| 400 | `limit must be specified once` | `limit` was repeated. | Send it once. |
| 400 | `limit must be between 1 and 100` | `limit` is not an integer from 1 to 100. | Fix `limit`. |
| 400 | `invalid pagination cursor` | `cursor` is not a value returned by a previous response. | Restart pagination without a cursor. |
| 401 | `unauthorized` | Missing or wrong API key. | Fix the credential. |
| 500 | `failed to construct pagination response` | The next-page cursor could not be encoded. | Retry. |
| 503 | `event listing temporarily unavailable` | The database query failed. | Retry with backoff. |

### `GET /v1/events/{id}` (inspect an event)

| Status | `error` | Cause | Client handling |
| --- | --- | --- | --- |
| 400 | `invalid event ID` | `{id}` is not a valid ObjectID. | Fix the ID. |
| 401 | `unauthorized` | Missing or wrong API key. | Fix the credential. |
| 404 | `event not found` | No event has this ID. | Do not retry. |
| 503 | `event inspection temporarily unavailable` | The database lookup failed. | Retry with backoff. |

### `GET /v1/events/{id}/attempts` (delivery attempt history)

| Status | `error` | Cause | Client handling |
| --- | --- | --- | --- |
| 400 | `invalid event ID` | `{id}` is not a valid ObjectID. | Fix the ID. |
| 400 | `limit must be specified once` | `limit` was repeated. | Send it once. |
| 400 | `limit must be between 1 and 100` | `limit` is not an integer from 1 to 100. | Fix `limit`. |
| 400 | `invalid pagination cursor` | `cursor` is not a value returned by a previous response. | Restart pagination without a cursor. |
| 401 | `unauthorized` | Missing or wrong API key. | Fix the credential. |
| 404 | `event not found` | No event has this ID. | Do not retry. |
| 500 | `failed to construct pagination response` | The next-page cursor could not be encoded. | Retry. |
| 503 | `attempt history temporarily unavailable` | A database lookup or query failed. | Retry with backoff. |

### `GET /v1/events/{id}/deliveries` (delivery state per endpoint)

| Status | `error` | Cause | Client handling |
| --- | --- | --- | --- |
| 400 | `invalid event ID` | `{id}` is not a valid ObjectID. | Fix the ID. |
| 401 | `unauthorized` | Missing or wrong API key. | Fix the credential. |
| 404 | `event not found` | No event has this ID. | Do not retry. |
| 503 | `delivery state temporarily unavailable` | A database lookup or query failed. | Retry with backoff. |

### Endpoint routes

Applies to `POST /v1/endpoints`, `GET /v1/endpoints`, `GET|PATCH|DELETE /v1/endpoints/{id}` and `GET /v1/endpoints/{id}/health`.

| Status | `error` | Routes | Cause | Client handling |
| --- | --- | --- | --- | --- |
| 400 | `invalid endpoint ID` | all `{id}` routes | `{id}` is not a valid ObjectID. | Fix the ID. |
| 400 | `invalid request body` | `POST`, `PATCH` | The body is not valid JSON, contains unknown fields, or has wrongly typed fields. | Fix the body. |
| 400 | varies (see [Endpoint validation messages](#endpoint-validation-messages)) | `POST` | A field failed validation. | Fix the field named in the message. |
| 401 | `unauthorized` | all | Missing or wrong API key. | Fix the credential. |
| 404 | `endpoint not found` | `GET`, `PATCH`, `DELETE` by ID, `health` | No endpoint has this ID. | Do not retry. |
| 409 | `endpoint name already exists` | `POST`, `PATCH` | The name is already used by an active endpoint. | Choose another name. |
| 503 | `endpoint create temporarily unavailable` | `POST` | A database or other non-validation failure. | Retry with backoff. |
| 503 | `endpoint list temporarily unavailable` | `GET /v1/endpoints` | Same. | Retry with backoff. |
| 503 | `endpoint get temporarily unavailable` | `GET /v1/endpoints/{id}`, `health` | Same. | Retry with backoff. |
| 503 | `endpoint update temporarily unavailable` | `PATCH` | Same. See the note below about validation failures. | Retry with backoff. |
| 503 | `endpoint disable temporarily unavailable` | `DELETE` | Same. | Retry with backoff. |
| 503 | `endpoint health temporarily unavailable` | `GET .../health` | The health statistics query failed. | Retry with backoff. |

Note on `PATCH /v1/endpoints/{id}`: the merged endpoint is validated with the same rules as creation, but a validation failure there is not mapped to 400. The handler currently returns `503` with `endpoint update temporarily unavailable` for it, so a persistent 503 on `PATCH` can mean the new values are invalid rather than that the service is down. Compare your values against the validation rules below before retrying.

#### Endpoint validation messages

Returned as `400` by `POST /v1/endpoints`, from `Endpoint.Validate` in `internal/endpoints/endpoint.go`:

| `error` | Rule |
| --- | --- |
| `endpoint name is required` | `name` is empty after trimming. |
| `endpoint name must be at most 200 characters` | `name` is longer than 200 characters. |
| `endpoint URL is required` | `url` is empty after trimming. |
| `max_attempts must be between 1 and 20` | `max_attempts` is outside 1 to 20 (it is required; omitting it counts as 0). |
| `rate_limit_rps must be 0 (unlimited) or between 1 and 1000` | `rate_limit_rps` is negative or above 1000. |
| `endpoint URL must be an absolute HTTP(S) URL without user information` | The URL does not parse, has no host, uses a scheme other than `http` or `https`, or contains user info. |
| `endpoint URL must not target a private, loopback, or link-local address` | The host is a literal private, loopback or link-local IP address. Hostnames are not resolved. |
| `signing secret must contain at least 32 characters` | A non-empty `signing_secret` is shorter than 32 characters. |

### Health and metrics routes

These do not use the `{"error": ...}` shape for every failure.

| Route | Status | Body | Cause |
| --- | --- | --- | --- |
| `GET /healthz` | 405 | `{"error": "method not allowed"}` | Method other than `GET`. |
| `GET /readyz` | 405 | `{"error": "method not allowed"}` | Method other than `GET`. |
| `GET /readyz` | 503 | `{"status": "not_ready"}` | MongoDB did not answer a ping, the index check failed, or a required index is missing. Retry; orchestrators should keep the instance out of rotation. |
| `GET /v1/health` | 405 | `{"error": "method not allowed"}` | Method other than `GET`. |
| `GET /v1/health` | 503 | service health object with `"status": "down"` and `"mongo": "unreachable"` | MongoDB is unreachable. |
| `GET /metrics` | 405 | plain text `method not allowed` | Method other than `GET`. |
| `GET /metrics` | 401 | plain text `unauthorized` | Missing or wrong API key. |
| `GET /metrics` | 503 | plain text `metrics temporarily unavailable` | The metrics snapshot query failed. Retry. |

## Responses that are not `{"error": ...}`

- **Plain-text 404** (`404 page not found`): returned by `POST /v1/events/{id}/replay` for malformed replay paths, and by the Go HTTP router for paths that match no route.
- **Plain-text 405** (`Method Not Allowed`, with an `Allow` header): returned by the router when a route that is registered for specific methods (the `/v1/endpoints...` routes) is called with another method. Routes that check the method themselves return the JSON body shown above.
- **Plain-text bodies on `/metrics`**, as listed in the table above.
- **`/readyz` and `/v1/health` 503 bodies**, as listed in the table above.

## Notes

- `500` and `503` (and some `409` cases, as noted) can change on a later attempt without changing the request; `400`, `401`, `404`, `405` and `413` fail the same way until the request or credential changes.
- Status codes and messages above come from the current source. If you change a handler, update this page and the `@Failure` annotations together (the OpenAPI files under `docs/` are generated from them by swag).
