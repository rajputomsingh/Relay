# Relay CLI

The Relay CLI is a local development and management tool for interacting with your Relay instance. It supports API authentication, endpoint management, event ingestion, and real-time event forwarding to local webhook handlers.

## Installation

Build the CLI from the repository root:

```powershell
go build -o relay-cli.exe ./cmd/relay-cli
```

Run the CLI:

```powershell
.\relay-cli.exe --help
```

## Authentication

Authenticate the CLI using your Relay API key:

```powershell
relay-cli login --api-key <your-api-key>
```

By default, the CLI connects to:

```text
http://localhost:8080
```

The configuration is stored at:

```text
~/.relay/config.json
```

On Unix-like systems, the configuration file is created with restrictive permissions (`0600`).

## Endpoint Management

### List endpoints

Retrieve the endpoints configured for your Relay instance:

```powershell
relay-cli endpoints list
```

### Create an endpoint

Register a new webhook endpoint:

```powershell
relay-cli endpoints create `
  --name orders `
  --url https://example.com/hook `
  --secret whsec_...
```

### Check endpoint health

Retrieve delivery health for an endpoint:

```powershell
relay-cli endpoints health ep_example
```

Example output:

```text
success_rate_1h: 95
attempts_1h: 100
successes_1h: 95
failures_1h: 5
consecutive_failures: 0
last_success_at: 2026-11-17T09:30:00Z
last_failure_at: 2026-11-17T09:00:00Z
```

The success rate is a percentage from 0 to 100. The rate and attempt counts cover
the past hour; consecutive failures and the latest timestamps reflect delivery
history. Timestamps use RFC 3339 format and appear as `—` when no corresponding
delivery exists. Use `relay-cli endpoints health --help` for command help.

## Sending Events

Send events to Relay using the CLI:

```powershell
relay-cli events send `
  --source my-app `
  --type user.created `
  --data '{"user_id":"u_1"}'
```

### Custom Idempotency Key

Specify a unique event identifier using `--unique-id`:

```powershell
relay-cli events send `
  --source my-app `
  --type user.created `
  --unique-id evt-001 `
  --data '{"user_id":"u_1"}'
```

If `--unique-id` is omitted, the CLI automatically generates one.

## Listening for Events

The `listen` command provides a local webhook forwarding workflow, similar to `stripe listen`.

Forward incoming Relay events to your local application:

```powershell
relay-cli listen --forward-to http://localhost:3000/webhook
```

If your endpoint requires signature verification, provide its webhook secret:

```powershell
relay-cli listen `
  --forward-to http://localhost:3000/webhook `
  --secret whsec_...
```

Each forwarded request includes the following headers:

| Header              | Description                        |
| ------------------- | ---------------------------------- |
| `X-Relay-Event-ID`  | Unique identifier of the event     |
| `X-Relay-Timestamp` | Unix timestamp in seconds          |
| `X-Relay-Signature` | HMAC-SHA256 signature, hex-encoded |

### Signature Verification

When `--secret` is provided, Relay signs forwarded requests using HMAC-SHA256.

The signature is calculated over:

```text
timestamp.payload
```

The resulting hexadecimal signature is sent through `X-Relay-Signature`.

Your local application can use this signature to verify the authenticity and integrity of forwarded requests.

Press `Ctrl+C` to stop listening.

## Local Development Workflow

A typical local development session involves running Relay, starting a webhook receiver, and forwarding events.

**1. Start your local Relay instance**

```powershell
go run ./cmd/relay
```

**2. Start a local webhook receiver**

Run your application on port `3000` with a handler configured for `/webhook`.

**3. Authenticate the CLI**

```powershell
relay-cli login --api-key test-key-123
```

**4. List configured endpoints**

```powershell
relay-cli endpoints list
```

**5. Send a test event**

```powershell
relay-cli events send `
  --source my-app `
  --type user.created `
  --data '{"user_id":"u_1"}'
```

**6. Forward events to your local application**

```powershell
relay-cli listen --forward-to http://localhost:3000/webhook
```

## Command Reference

| Command                      | Description                                    |
| ---------------------------- | ---------------------------------------------- |
| `relay-cli --help`           | Display available commands                     |
| `relay-cli login`            | Authenticate using an API key                  |
| `relay-cli endpoints list`   | List registered endpoints                      |
| `relay-cli endpoints health <id>` | Show endpoint delivery health             |
| `relay-cli endpoints create` | Register a webhook endpoint                    |
| `relay-cli events send`      | Publish an event                               |
| `relay-cli listen`           | Forward incoming events to a local application |

## Building from Source

Build all Relay packages:

```powershell
go build ./...
```

Build the standalone CLI binary:

```powershell
go build -o bin/relay-cli.exe ./cmd/relay-cli
```

Run the CLI:

```powershell
.\bin\relay-cli.exe --help
```

## Quality Checks

Run the project linter:

```powershell
golangci-lint run --config=.golangci.yml ./...
```

The CLI should compile successfully and pass the configured lint checks before submitting changes.

---

Part of the [Relay](https://github.com/rajputomsingh/Relay) project.

## Shell completion

Relay CLI supports shell completion for PowerShell, Bash, Zsh, and Fish.

### PowerShell

Add this to your PowerShell profile (`$PROFILE`):

```powershell
relay-cli completion powershell | Out-String | Invoke-Expression
```

### Bash

```bash
source <(relay-cli completion bash)
```

To persist:

```bash
relay-cli completion bash > /etc/bash_completion.d/relay-cli
```

### Zsh

```zsh
relay-cli completion zsh > "${fpath[1]}/_relay-cli"
```

### Fish

```fish
relay-cli completion fish > ~/.config/fish/completions/relay-cli.fish
```

After installation, restart your shell and use Tab completion.
