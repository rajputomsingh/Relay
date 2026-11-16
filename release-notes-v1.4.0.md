# Relay v1.4.0 - relay-cli Complete

Relay v1.4.0 introduces the complete `relay-cli`, making it easier to manage endpoints, publish events, inspect delivery history, replay events, and forward webhooks to local development environments.

This release also includes reliability improvements, SDK updates, health monitoring, and documentation improvements.

## What's New

### relay-cli

The new Relay CLI provides a terminal interface for interacting with your Relay instance.

**Features**

- API key authentication with `relay-cli login`
- Create, list, update, and delete endpoints
- Send events directly from the terminal
- List event history
- Replay previously accepted events
- Listen for incoming webhook deliveries locally
- Forward webhook requests to a local development server
- HMAC signature generation for forwarded webhook requests

**Quick Start**

```bash
relay-cli login --api-key <your-api-key>

relay-cli endpoints list

relay-cli events send \
  --source my-app \
  --type user.created \
  --data '{"user_id":"u_1"}'

relay-cli listen \
  --forward-to http://localhost:3000/webhook \
  --secret whsec_...
```

### Cross-platform binaries

Prebuilt CLI binaries are available for:

| Platform | Architecture | Binary                        |
| -------- | ------------ | ----------------------------- |
| Linux    | AMD64        | `relay-cli-linux-amd64`       |
| Linux    | ARM64        | `relay-cli-linux-arm64`       |
| macOS    | AMD64        | `relay-cli-darwin-amd64`      |
| macOS    | ARM64        | `relay-cli-darwin-arm64`      |
| Windows  | AMD64        | `relay-cli-windows-amd64.exe` |

Download the appropriate binary from the [v1.4.0 release page](https://github.com/rajputomsingh/Relay/releases/tag/v1.4.0).

SHA256 checksums are included in `checksums.txt`.

### Reliability and delivery improvements

- Improved delivery worker reliability.
- Improved cancellation handling and test coverage.
- Fixed a data race in concurrent execution.
- Improved delivery health reporting.
- Added delivery latency percentile monitoring.
- Improved endpoint lifecycle handling.

### SDK and documentation

- Added `DeleteEndpoint` support to the Go SDK.
- Improved SDK documentation.
- Expanded CLI documentation.
- Added shell completion documentation for Bash, Zsh, Fish, and PowerShell.
- Updated project documentation and contributor guidance.

### Health and observability

- Improved service-level health reporting.
- Extended worker health visibility.
- Continued improvements to delivery observability.

## Installation

Download the binary for your platform from:

https://github.com/rajputomsingh/Relay/releases/tag/v1.4.0

For Windows, rename the downloaded executable to `relay-cli.exe` if desired, then run:

```powershell
.\relay-cli.exe --help
```

For Linux and macOS:

```bash
chmod +x relay-cli-*
./relay-cli-linux-amd64 --help
```

## Validation

The release was validated with:

```bash
go test ./...
go vet ./...
go build ./...
golangci-lint run --config=.golangci.yml ./...
```

The CLI was also manually tested through the complete delivery flow:

1. Authenticate using the CLI.
2. Create and configure an endpoint.
3. Submit an event.
4. Process the event through the delivery worker.
5. Forward the webhook to a local receiver.
6. Verify the HMAC signature and delivery response.

Cross-platform release binaries and SHA256 checksums were generated.

## Known Limitations

- Relay currently operates with a single-tenant architecture.
- Endpoint deletion is implemented as a soft delete.
- A web dashboard is not yet available.
- Timestamp-based HMAC replay protection remains planned.

See the [multi-tenant architecture RFC](https://github.com/rajputomsingh/Relay/blob/main/docs/rfcs/0001-multi-tenant.md).

## What's Next

The next development priorities include:

- Multi-tenant architecture and tenant isolation.
- Dashboard and endpoint management UI.
- Timestamp-based HMAC replay protection.
- Further SDK improvements and automated releases.
- Continued improvements to delivery observability and reliability.

## Contributors

Thanks to everyone who contributed to this release, reported issues, reviewed changes, and helped improve Relay.

Special thanks to:

- [@sharanyashetty013-droid](https://github.com/sharanyashetty013-droid)
- [@jay23606](https://github.com/jay23606)

## Links

- [GitHub Repository](https://github.com/rajputomsingh/Relay)
- [Release v1.4.0](https://github.com/rajputomsingh/Relay/releases/tag/v1.4.0)
- [Full Changelog](https://github.com/rajputomsingh/Relay/compare/v1.3.0...v1.4.0)
- [CLI Documentation](https://github.com/rajputomsingh/Relay/blob/main/docs/cli.md)
- [Report an Issue](https://github.com/rajputomsingh/Relay/issues)

---

**Relay v1.4.0 is now available.**

Built for reliable webhook delivery.
