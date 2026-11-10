# relay-python-sdk

Official Python SDK for [Relay](https://github.com/rajputomsingh/Relay), a self-hosted webhook delivery engine.

Submit events, inspect delivery lifecycles, manage endpoints, and verify webhook signatures using a Python client.

## Installation

```bash
pip install relay-python-sdk
```

## Quick start

```python
from relay_python import Relay

with Relay(
    api_key="your-api-key",
    base_url="http://localhost:8080",
) as client:
    # Submit an event
    response = client.send_event(
        source="stripe",
        event_type="payment.completed",
        data={
            "amount": 5000,
            "currency": "usd",
        },
        idempotency_key="order-123",
    )

    print(response.status, response.event_id)

    # Inspect its lifecycle state
    event = client.inspect_event(response.event_id)
    print(event.delivery_status)
```

The `Relay` client supports context-manager usage for resource cleanup.

## Webhook signature verification

When an endpoint is configured with a signing secret, Relay signs outbound webhook requests using HMAC-SHA256 over:

```text
<timestamp>.<body>
```

Verify incoming requests before processing their payloads.

```python
from relay_python import verify_webhook_signature, SignatureError


def handle_webhook(request):
    try:
        verify_webhook_signature(
            secret=ENDPOINT_SIGNING_SECRET,
            timestamp_header=request.headers["X-Relay-Timestamp"],
            signature_header=request.headers["X-Relay-Signature"],
            body=request.body,  # Raw bytes, before JSON parsing
        )
    except SignatureError:
        return 401

    # Signature is valid and fresh.
    # Continue processing the webhook.
```

**Important:** Pass the original raw request body as bytes. Do not parse or re-serialize the JSON before signature verification.

The default timestamp freshness window is five minutes. The SDK exposes this through:

```python
from relay_python import DEFAULT_MAX_AGE_MS
```

## API reference

The Python SDK exposes the following client operations.

### Events

| Method             | Description                                |
| ------------------ | ------------------------------------------ |
| `send_event()`     | Submit an event to Relay.                  |
| `list_events()`    | List submitted events.                     |
| `inspect_event()`  | Retrieve an event's lifecycle information. |
| `replay_event()`   | Request replay of an event.                |
| `get_deliveries()` | Retrieve an event's delivery records.      |
| `get_attempts()`   | Retrieve an event's delivery attempts.     |

### Endpoints

| Method               | Description                    |
| -------------------- | ------------------------------ |
| `create_endpoint()`  | Register a webhook endpoint.   |
| `list_endpoints()`   | List configured endpoints.     |
| `get_endpoint()`     | Retrieve endpoint details.     |
| `update_endpoint()`  | Update endpoint configuration. |
| `disable_endpoint()` | Disable an endpoint.           |

### Health

| Method     | Description              |
| ---------- | ------------------------ |
| `health()` | Check service liveness.  |
| `ready()`  | Check service readiness. |

### Signature utilities

| Function                     | Description                                         |
| ---------------------------- | --------------------------------------------------- |
| `sign_webhook_body()`        | Generate a webhook signature.                       |
| `verify_webhook_signature()` | Verify a signature and enforce timestamp freshness. |

## Idempotency

Every `send_event()` call requires an `idempotency_key`.

Relay uses this key to prevent duplicate event acceptance when the same submission is retried.

The response differs depending on whether the event has already been accepted.

**First submission:**

```json
{
  "status": "accepted",
  "event_id": "event-id"
}
```

**Repeated submission with the same key:**

```json
{
  "status": "already_accepted",
  "idempotency_key": "order-123"
}
```

The repeated-submission response does not include `event_id`. Relay does not return the original event ID on this retry path.

When the event ID is needed after a retry, retrieve the event using `list_events()`.

Do not assume that `event_id` is present in every successful submission response.

## Versioning

This SDK follows semantic versioning.

Version `0.2.0` aligns the Python SDK with Relay's actual server API contract and corrects inconsistencies in earlier SDK models and request/response handling.

See [CHANGELOG.md](CHANGELOG.md) for breaking changes introduced since `0.1.x`.

## Relay

- **Repository:** [github.com/rajputomsingh/Relay](https://github.com/rajputomsingh/Relay)
- **Issues:** [Report a bug or request an improvement](https://github.com/rajputomsingh/Relay/issues)
- **Discussions:** [Join the Relay community](https://github.com/rajputomsingh/Relay/discussions)
