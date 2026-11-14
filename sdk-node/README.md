# Relay Node.js SDK

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

The official Node.js/TypeScript SDK for [Relay](https://github.com/rajputomsingh/Relay), an open-source webhook delivery infrastructure built for reliable event ingestion and delivery.

## Installation

```bash
npm install @rajputomsingh/relay-node
```

## Quick Start

```typescript
import { Relay } from "@rajputomsingh/relay-node";

const relay = new Relay({
  apiKey: process.env.RELAY_API_KEY!,
});

const submitted = await relay.sendEvent(
  {
    source: "my-app",
    event_type: "user.created",
    data: { userId: 123 },
  },
  "user-created-001",
);
if (!submitted.event_id) {
  throw new Error("Look up the event ID with listEvents() after an idempotent retry.");
}
const eventId = submitted.event_id;
```

## Endpoints and deliveries

These examples reuse `relay` and `eventId` from Quick Start. See the [API reference](../README.md#api-reference) and [OpenAPI specification](../docs/swagger.yaml) for more detail.

### List endpoints

```typescript
const { endpoints, count } = await relay.listEndpoints();
console.log(`${count} endpoints`);
for (const endpoint of endpoints) {
  console.log(endpoint.id, endpoint.name, endpoint.enabled);
}
```

### Create an endpoint

```typescript
const endpoint = await relay.createEndpoint({
  name: "orders",
  url: "https://example.com/webhooks/orders",
  event_filter: { sources: ["orders"], event_types: ["order.created"] },
  max_attempts: 5,
});
console.log(endpoint.id, endpoint.has_signing_secret);
```

### Update an endpoint

```typescript
const updated = await relay.updateEndpoint(endpoint.id, {
  enabled: true,
  rate_limit_rps: 10,
});
console.log(updated.updated_at);
```

### Read endpoint health

```typescript
const health = await relay.getEndpointHealth(endpoint.id);
console.log({
  success_rate_1h: health.success_rate_1h,
  attempts_1h: health.attempts_1h,
  last_success_at: health.last_success_at,
  last_failure_at: health.last_failure_at,
});
```

With no delivery history, the response has zero counts and explicit null timestamps and latency percentiles:

```json
{
  "success_rate_1h": 0, "attempts_1h": 0, "successes_1h": 0,
  "failures_1h": 0, "consecutive_failures": 0,
  "last_success_at": null, "last_failure_at": null,
  "p50_latency_ms": null, "p95_latency_ms": null, "p99_latency_ms": null
}
```

### Replay an event

```typescript
const replay = await relay.replayEvent(eventId);
if (replay.status === "replay_queued") {
  console.log(replay.event_id, replay.deliveries_requeued);
}
```

### Inspect event deliveries

```typescript
const event = await relay.inspectEvent(eventId);
const { deliveries } = await relay.listDeliveries(event.id);
for (const delivery of deliveries) {
  console.log(delivery.endpoint_id, delivery.status, delivery.attempts);
}
```

## Error Handling

Failed requests (non-2xx responses) throw a `RelayAPIError` that exposes the HTTP `status`, the parsed response `body`, and the error `message`.

Common statuses are mapped to typed subclasses, all of which extend `RelayAPIError`:

| Class              | Status          |
| ------------------ | --------------- |
| `BadRequestError`  | `400`           |
| `UnauthorizedError`| `401`           |
| `NotFoundError`    | `404`           |
| `RateLimitedError` | `429`           |
| `ServerError`      | `5xx`           |
| `RelayAPIError`    | any other status |

```typescript
import {
  NotFoundError,
  RateLimitedError,
  RelayAPIError,
  ServerError,
  UnauthorizedError,
} from "@rajputomsingh/relay-node";

try {
  await relay.getEndpoint(endpoint.id);
} catch (err) {
  if (err instanceof UnauthorizedError) console.error("Check the API key"); // 401
  else if (err instanceof NotFoundError) console.error("Endpoint not found"); // 404
  else if (err instanceof ServerError) console.error("Retry after a 5xx response"); // e.g. 503
  else if (err instanceof RateLimitedError) console.error("Back off and retry");
  else if (err instanceof RelayAPIError) {
    console.error(err.status, err.message, err.body);
  }
}
```

Because every subclass extends `RelayAPIError`, a single `instanceof RelayAPIError` check still catches all API errors. The existing status helpers (`isNotFound()`, `isConflict()`, `isUnauthorized()`, `isRateLimited()`, `isServerError()`) continue to work on every subclass.

## Webhook Signatures

```typescript
import { verifyWebhookSignature, SignatureError } from "@rajputomsingh/relay-node";

try {
  verifyWebhookSignature(secret, timestamp, signature, rawBody);
} catch (err) {
  if (err instanceof SignatureError) {
    // Invalid or stale signature.
  }
}
```

## Development

```bash
pnpm install
pnpm test           # run the unit test suite
pnpm typecheck      # tsc --noEmit
pnpm build          # emit dist/
```
