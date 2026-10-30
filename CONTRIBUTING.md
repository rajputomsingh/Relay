# Contributing to Relay

Thank you for your interest in contributing to Relay! Relay is built in the open and welcomes developers of all experience levels.

## Who this is for

We especially welcome contributions from developers interested in Go, distributed systems, webhook infrastructure, MongoDB, observability, and developer tooling.

You do not need prior open-source experience.

## Ways to contribute

| Type | Examples |
|------|----------|
| Code | Bug fixes, new features, tests, benchmarks |
| Docs | API docs, tutorials, examples, architecture guides |
| SDKs | Python, Go, Rust, PHP clients |
| Infrastructure | Docker, CI, monitoring |
| Community | Answering questions, triaging issues, reviewing PRs |

Look for issues labeled good first issue or help wanted.

## Development Setup

Prerequisites: Go (matching go.mod), Git, MongoDB for integration tests.

git clone https://github.com/YOUR_USERNAME/relay.git
cd relay
git switch -c feat/your-change
go mod download

Configure local env from .env.example. Never commit secrets.

## Running MongoDB integration tests

Option A: local MongoDB
  export MONGODB_TEST_URI="mongodb://localhost:27017"
  go test ./internal/delivery -count=1

Option B: Docker
  docker compose up -d mongodb
  MONGODB_TEST_URI="mongodb://localhost:27017" go test ./internal/delivery -count=1

## Code Guidelines

- Follow standard Go conventions
- Format with gofmt
- Handle errors explicitly
- Keep PRs focused
- Add tests for behavior changes

## Validation

Run before submitting:

  gofmt -w .
  go test ./...
  go vet ./...
  go build ./...
  git diff --check

All must pass. CI rejects PRs that fail.

## Commit Messages

Follow Conventional Commits: feat, fix, docs, test, refactor, chore, perf.

## Branch naming

  feat/short-description
  fix/short-description
  docs/short-description
  test/short-description

## Pull Requests

1. Explain the problem and proposed solution
2. Describe changes
3. Include testing steps
4. Link related issues (Closes #123)
5. Mention breaking changes

Keep PRs focused — one logical change per PR.

## What to expect

- First response within 48 hours (target)
- Approval to merge typically within 1 week for small changes
- If you do not hear back in 5 days, ping the PR thread

## Architecture overview

1. Ingestion (internal/handlers/) — POST /v1/events accepts JSON, validates auth, enforces Idempotency-Key, writes to MongoDB
2. Matching (internal/events/) — Events matched against enabled endpoints by source + event_type
3. Delivery worker (internal/delivery/) — Claims pending deliveries with lease tokens, sends HTTP, records attempts
4. Retry and recovery — Failed deliveries retry with exponential backoff; crashed workers release leases

## Bug Reports

Include reproduction steps, expected vs actual behavior, sanitized logs.

## Security

See SECURITY.md for private disclosure.

## License

By contributing, you agree your contributions are distributed under the project's Apache License 2.0. See LICENSE.