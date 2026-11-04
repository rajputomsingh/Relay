# Relay Go SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/rajputomsingh/relay-go.svg)](https://pkg.go.dev/github.com/rajputomsingh/relay-go)
[![Go Version](https://img.shields.io/github/go-mod/go-version/rajputomsingh/relay-go)](https://github.com/rajputomsingh/relay-go)
[![License](https://img.shields.io/github/license/rajputomsingh/relay-go)](LICENSE)

The official Go SDK for [Relay](https://github.com/rajputomsingh/relay), an open-source webhook delivery infrastructure built for reliable event ingestion and delivery.

Relay provides at-least-once delivery guarantees, asynchronous processing, automatic retries, and delivery lifecycle management. This SDK gives Go applications a simple, context-aware interface for publishing events to Relay.

## Features

- **Simple API:** Send events with a minimal, typed Go interface.
- **Context-aware:** Every API request accepts `context.Context` for cancellation and timeout control.
- **Automatic retries:** Exponential backoff with jitter for network failures, HTTP 429, and HTTP 5xx responses.
- **Idempotency support:** Attach an idempotency key to prevent duplicate event ingestion.
- **Typed errors:** Inspect API failures using `*relay.APIError`.
- **Configurable client:** Customize the API endpoint and underlying HTTP client.
- **Standard library friendly:** Built around Go's standard HTTP and context packages.

## Requirements

- Go 1.18 or later.
- A running Relay instance.
- A valid Relay API key.

## Installation

Install the SDK using Go modules:

```bash
go get github.com/rajputomsingh/relay-go
```

Import it into your application:

```go
import "github.com/rajputomsingh/relay-go"
```

## Quick Start

The following example initializes the client and sends an event to Relay.

```go
package main

import (
    "context"
    "fmt"
    "log"

    relay "github.com/rajputomsingh/relay-go"
)

func main() {
    // Initialize the client with your Relay API key.
    // The default API URL is http://localhost:8080.
    client := relay.NewClient("your-api-key")

    // Send an event.
    resp, err := client.SendEvent(
        context.Background(),
        relay.Event{
            Source:    "my-app",
            EventType: "user.created",
            Data: map[string]interface{}{
                "userId": 123,
                "email":  "user@example.com",
            },
        },
        "idempotency-key-123",
    )

    if err != nil {
        log.Fatalf("failed to send event: %v", err)
    }

    fmt.Printf(
        "Event accepted: %s (status: %s)\n",
        resp.ID,
        resp.Status,
    )
}
```

The SDK returns an event response when Relay accepts the event for processing.

**Note:** An accepted event does not necessarily mean the downstream webhook has already been delivered. Delivery happens asynchronously.

## Configuration

The SDK provides functional options for customizing the client.

By default, the client connects to:

```text
http://localhost:8080
```

### Custom API URL

Use `WithBaseURL` to connect to a different Relay instance.

```go
client := relay.NewClient(
    "your-api-key",
    relay.WithBaseURL("https://relay.yourdomain.com"),
)
```

### Custom HTTP Client

Use `WithHTTPClient` to provide your own `http.Client`.

This is useful when configuring custom timeouts, transport settings, or proxies.

```go
package main

import (
    "net/http"
    "time"

    relay "github.com/rajputomsingh/relay-go"
)

func main() {
    httpClient := &http.Client{
        Timeout: 5 * time.Second,
    }

    client := relay.NewClient(
        "your-api-key",
        relay.WithHTTPClient(httpClient),
    )

    _ = client
}
```

### Available Options

| Option                         | Description                          |
| ------------------------------ | ------------------------------------ |
| `WithBaseURL(string)`          | Overrides the default Relay API URL. |
| `WithHTTPClient(*http.Client)` | Overrides the default HTTP client.   |

## Sending Events

Use `SendEvent` to publish an event to Relay.

```go
resp, err := client.SendEvent(
    ctx,
    relay.Event{
        Source:    "billing-service",
        EventType: "payment.completed",
        Data: map[string]interface{}{
            "paymentId": "pay_123",
            "amount":    4999,
            "currency":  "INR",
        },
    },
    "payment-completed-pay-123",
)

if err != nil {
    log.Printf("event submission failed: %v", err)
    return
}

fmt.Printf("Event ID: %s\n", resp.ID)
fmt.Printf("Status: %s\n", resp.Status)
```

### Event Fields

| Field       | Description                                                |
| ----------- | ---------------------------------------------------------- |
| `Source`    | Identifies the application or service producing the event. |
| `EventType` | Describes the type of event being published.               |
| `Data`      | Contains the event payload.                                |

## Idempotency

The SDK supports idempotency keys when sending events.

An idempotency key helps prevent duplicate event ingestion when the same logical request is submitted multiple times.

```go
resp, err := client.SendEvent(
    ctx,
    relay.Event{
        Source:    "my-app",
        EventType: "user.created",
        Data: map[string]interface{}{
            "userId": 123,
        },
    },
    "user-created-123",
)
```

The SDK sends the supplied key using the `Idempotency-Key` HTTP header.

Use a stable key for retries of the same logical operation.

For example:

```text
user-created-123
payment-completed-pay-123
order-created-order-456
```

Avoid generating a new key every time you retry the same operation. Doing so defeats the purpose of idempotency.

To skip idempotency, pass an empty string:

```go
resp, err := client.SendEvent(ctx, event, "")
```

**Important:** Idempotency protects event ingestion according to Relay's server-side implementation. It does not automatically guarantee that every downstream consumer is idempotent.

## Error Handling

The SDK returns a typed `*relay.APIError` for non-2xx responses from the Relay API.

Use Go's `errors.As` to inspect API errors and handle specific HTTP status codes.

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"

    relay "github.com/rajputomsingh/relay-go"
)

func sendEvent(client *relay.Client, event relay.Event) {
    resp, err := client.SendEvent(
        context.Background(),
        event,
        "",
    )

    if err != nil {
        var apiErr *relay.APIError

        if errors.As(err, &apiErr) {
            fmt.Printf(
                "API Error: Status=%d Message=%s\n",
                apiErr.StatusCode,
                apiErr.Message,
            )

            switch apiErr.StatusCode {
            case 400:
                log.Println("Invalid event payload")
            case 401:
                log.Println("Invalid API key")
            case 429:
                log.Println("Rate limit exceeded")
            default:
                log.Println("Relay API request failed")
            }

            return
        }

        // Network, transport, or other client-side error.
        log.Printf("Request failed: %v", err)
        return
    }

    fmt.Printf("Event accepted: %s\n", resp.ID)
}
```

### Error Categories

| Error type        | Description                                                  |
| ----------------- | ------------------------------------------------------------ |
| `*relay.APIError` | An HTTP error response returned by Relay.                    |
| Network error     | A failure while establishing or completing the HTTP request. |
| Context error     | The request was canceled or its context deadline expired.    |

For API errors, inspect `StatusCode` and `Message` to determine the appropriate response.

## Automatic Retries

The SDK automatically retries requests for supported transient failures.

By default, retries are attempted for:

- Network failures.
- `429 Too Many Requests`.
- `5xx` server errors.

The default retry configuration is:

| Setting          | Default                         |
| ---------------- | ------------------------------- |
| Maximum retries  | 3                               |
| Initial backoff  | 500ms                           |
| Maximum backoff  | 5 seconds                       |
| Backoff strategy | Exponential backoff with jitter |

### Custom Retry Configuration

You can customize retry behavior through the client's `RetryConfig`.

```go
package main

import (
    "time"

    relay "github.com/rajputomsingh/relay-go"
)

func main() {
    client := relay.NewClient("your-api-key")

    client.RetryConfig = relay.RetryConfig{
        MaxRetries: 5,
        MinWait:    1 * time.Second,
        MaxWait:    10 * time.Second,
    }

    _ = client
}
```

This configuration allows you to adjust retry limits and backoff intervals based on your application's requirements.

**Important:** Retrying an event submission is safest when you reuse the same idempotency key for the same logical operation.

## Context and Timeouts

Every API call accepts a `context.Context`.

Use contexts to control request lifetimes, cancel operations, and enforce deadlines.

```go
ctx, cancel := context.WithTimeout(
    context.Background(),
    10*time.Second,
)
defer cancel()

resp, err := client.SendEvent(ctx, event, "event-123")

if err != nil {
    log.Printf("event submission failed: %v", err)
    return
}

fmt.Println("Accepted:", resp.ID)
```

This ensures the request does not continue beyond the specified context deadline.

## Complete Example

A minimal application using a configured client, timeout, and idempotency key:

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "net/http"
    "time"

    relay "github.com/rajputomsingh/relay-go"
)

func main() {
    client := relay.NewClient(
        "your-api-key",
        relay.WithBaseURL("http://localhost:8080"),
        relay.WithHTTPClient(&http.Client{
            Timeout: 10 * time.Second,
        }),
    )

    ctx, cancel := context.WithTimeout(
        context.Background(),
        15*time.Second,
    )
    defer cancel()

    event := relay.Event{
        Source:    "order-service",
        EventType: "order.created",
        Data: map[string]interface{}{
            "orderId": "order-123",
            "amount":  2499,
            "currency": "INR",
        },
    }

    resp, err := client.SendEvent(
        ctx,
        event,
        "order-created-123",
    )

    if err != nil {
        var apiErr *relay.APIError

        if errors.As(err, &apiErr) {
            log.Printf(
                "Relay API error: status=%d message=%s",
                apiErr.StatusCode,
                apiErr.Message,
            )
        } else {
            log.Printf("Request error: %v", err)
        }

        return
    }

    fmt.Printf("Event accepted successfully\n")
    fmt.Printf("Event ID: %s\n", resp.ID)
    fmt.Printf("Status: %s\n", resp.Status)
}
```

## Development

Contributions to the Relay Go SDK are welcome.

### Clone the Repository

```bash
git clone https://github.com/rajputomsingh/relay-go.git
cd relay-go
```

### Install Dependencies

```bash
go mod tidy
```

### Run Tests

```bash
go test ./...
```

### Run Tests with the Race Detector

```bash
go test -race ./...
```

### Build

```bash
go build ./...
```

Before submitting a pull request, ensure tests pass and your changes are formatted:

```bash
gofmt -w .
go test ./...
go vet ./...
```

## Contributing

Contributions, bug reports, feature requests, and documentation improvements are welcome.

If you want to contribute:

1. Check the existing [issues](https://github.com/rajputomsingh/relay-go/issues).
2. Open an issue to discuss substantial changes.
3. Fork the repository.
4. Create a feature branch.
5. Submit a pull request with a clear description and relevant tests.

For core Relay infrastructure contributions, visit the [Relay repository](https://github.com/rajputomsingh/relay).

## Roadmap

The SDK currently focuses on event ingestion.

Potential future additions include:

- **Endpoint Management:** Create, list, retrieve, and delete webhook endpoints.
- **Delivery Inspection:** Retrieve delivery attempts and inspect their statuses.
- **Event Inspection:** Retrieve previously submitted events.
- **Advanced Configuration:** Additional retry and transport configuration.
- **Improved Observability:** Better visibility into request and delivery outcomes.

These are planned extensions, not necessarily available in the current SDK release.

## Related Projects

| Project                                                   | Description                           |
| --------------------------------------------------------- | ------------------------------------- |
| [Relay](https://github.com/rajputomsingh/relay)           | Core webhook delivery infrastructure. |
| [Relay Go SDK](https://github.com/rajputomsingh/relay-go) | Official Go client for Relay.         |

## License

This SDK is distributed under the same license as the core Relay project.

See the [LICENSE](LICENSE) file for details.
