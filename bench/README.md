# Delivery Benchmarks

Reproducible measurements of Relay's delivery pipeline. The harness
reuses the real `delivery.Worker` and the same MongoDB schema used in
production. It is not a separate delivery implementation.

## Prerequisites

- Go 1.22 or later.
- A reachable MongoDB instance. The default is `MONGODB_TEST_URI`.
- A shell that can run `go run`.

## Quick start

Start MongoDB:

```bash
docker run -d --name relay-mongo -p 27017:27017 mongo:7
```
