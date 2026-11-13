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

await relay.sendEvent(
  {
    source: "my-app",
    event_type: "user.created",
    data: { userId: 123 },
  },
  "user-created-001",
);
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
  Relay,
  RateLimitedError,
  RelayAPIError,
  ServerError,
} from "@rajputomsingh/relay-node";

try {
  await relay.sendEvent({ source: "my-app", event_type: "order.placed", data: {} });
} catch (err) {
  if (err instanceof RateLimitedError) {
    // Back off and retry later; err.status === 429.
  } else if (err instanceof ServerError) {
    // Relay is unhealthy; err.status >= 500.
  } else if (err instanceof RelayAPIError) {
    // Any other API failure, e.g. 403 or 409.
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
