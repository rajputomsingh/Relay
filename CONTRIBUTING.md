# Contributing to Relay

Thanks for your interest in contributing to Relay. Whether this is your first pull request or you are an experienced systems engineer, you're welcome here.

Relay is an open-source webhook delivery infrastructure project built with Go and MongoDB. Contributions that improve reliability, observability, security, documentation, testing, and developer experience are especially valuable.

## Who this is for

Relay welcomes:

- First-time open-source contributors who want a clearly scoped first issue.
- Go developers interested in HTTP services, concurrency, persistence, and distributed systems.
- Developers working with webhooks, retries, idempotency, and event-driven systems.
- Contributors who can improve documentation, SDK examples, tests, or deployment workflows.

You do not need to be an expert in every part of Relay. Start with an issue that matches your experience, and ask questions whenever something is unclear.

## First-time contributor quickstart

If this is your first pull request anywhere, follow these five steps. Estimated time to prepare and submit a first contribution: **30–90 minutes**, depending on the issue and your environment.

### Step 1 — Pick an issue (2 minutes)

Open the [good first issue label](https://github.com/rajputomsingh/relay/labels/good%20first%20issue). Look for issues that explain:

- The **specific file path** you'll touch.
- The **concrete expected change**.
- The **estimated time** required.
- The **test command** used to verify your work.

If you're not sure which issue fits, leave a comment:

> I'd like to take this. Is this a good fit for a first PR?

Please wait for maintainer confirmation before starting work on an issue that may already be assigned.

### Step 2 — Fork and clone (5 minutes)

1. Click **Fork** on [the Relay repository](https://github.com/rajputomsingh/relay).
2. Clone your fork and configure the upstream remote:

```bash
git clone https://github.com/YOUR_USERNAME/relay.git
cd relay
git remote add upstream https://github.com/rajputomsingh/relay.git
git switch -c docs/your-change-here
```

Use a branch name that describes the work. See [Branch naming](#branch-naming).

### Step 3 — Set up the environment (10–20 minutes)

Download Go dependencies:

```bash
go mod download
```

Copy the example environment file:

```bash
cp .env.example .env
```

Edit `.env` and configure the values required by your local setup, including:

- `MONGODB_URI`
- `RELAY_API_KEY` (minimum 32 characters)

The server loads `.env` from the current working directory on startup. Values already exported in your shell always take precedence over `.env`, so you can override any single value without editing the file. If an existing `.env` has invalid syntax, startup fails with an error naming the file.

If you're using Windows PowerShell and `cp` is unavailable, use:

```powershell
Copy-Item .env.example .env
```

You do not need MongoDB to run the unit tests. Integration tests require a reachable MongoDB instance.

If you do not have MongoDB installed, start the Compose service:

```bash
docker compose up -d mongodb
```

If setup fails, comment on your issue or open a Discussion. Setup problems are useful feedback because they can reveal documentation gaps.

### Step 4 — Make and validate the change (30–60 minutes)

Follow the issue description and keep the change focused. Before opening a pull request, run:

```bash
go test ./...
go vet ./...
go build ./...
git diff --check
```

All four checks should pass. If a command fails, include the relevant error output in your issue or pull request so someone can help diagnose it.

### Step 5 — Open the pull request (5 minutes)

Review the files you intend to commit, then stage and commit your changes:

```bash
git status
git add -A
git diff --cached
git commit -m "docs: describe your change"
git push -u origin docs/your-change-here
```

Use a commit message that follows [Conventional Commits](https://www.conventionalcommits.org/). Do not stage `.env`, credentials, generated files, or unrelated changes.

After pushing, open your fork on GitHub and select **Compare & pull request**. Submit the pull request against the upstream `main` branch. Explain what changed, why it was needed, and which checks you ran.

**First-response target:** within 48 hours. If you have not heard back after 5 days, politely ping the issue or pull request thread.

### What if I get stuck?

Comment on your issue, reply in the pull request thread, or open a [Discussion](https://github.com/rajputomsingh/relay/discussions). No question is too basic. If a step is confusing, explaining where you got stuck helps improve the contributor experience for the next person too.

## Ways to contribute

You can contribute in several ways:

| Contribution  | Examples                                                              |
| ------------- | --------------------------------------------------------------------- |
| Code          | Bug fixes, delivery reliability, API improvements, worker behavior    |
| Tests         | Unit tests, MongoDB integration tests, failure and recovery scenarios |
| Documentation | Setup guides, API examples, deployment notes, troubleshooting         |
| SDKs          | Node.js/TypeScript and Python examples, usability improvements        |
| Observability | Metrics, dashboards, logs, and operational guidance                   |
| Community     | Issue triage, reproductions, design discussions, and reviews          |

**Looking for something to work on?** Browse the issue labels:

| Label                                                                                    | Meaning                           | Time estimate      |
| ---------------------------------------------------------------------------------------- | --------------------------------- | ------------------ |
| [`good first issue`](https://github.com/rajputomsingh/relay/labels/good%20first%20issue) | No prior context needed           | 1–4 hours          |
| [`help wanted`](https://github.com/rajputomsingh/relay/labels/help%20wanted)             | Needs architectural understanding | 4–8 hours          |
| [`documentation`](https://github.com/rajputomsingh/relay/labels/documentation)           | Docs, examples, and guides        | 30 minutes–2 hours |
| [`advanced`](https://github.com/rajputomsingh/relay/labels/advanced)                     | For experienced contributors      | 1–3 days           |

If none of the current issues fit what you want to work on, open a [Discussion](https://github.com/rajputomsingh/relay/discussions) describing your idea before beginning a large change.

## Development Setup

### Prerequisites

| Tool        | Version                  | Required for                            |
| ----------- | ------------------------ | --------------------------------------- |
| **Go**      | Matching `go.mod`        | Building and unit tests                 |
| **Git**     | Recent version           | Cloning and pull requests               |
| **MongoDB** | 7.0+                     | Integration tests                       |
| **Docker**  | Recent version; optional | Running MongoDB locally through Compose |

You can run unit tests without MongoDB. Integration tests require a reachable MongoDB instance.

### Clone and configure

Fork the repository on GitHub, then clone your fork:

```bash
git clone https://github.com/YOUR_USERNAME/relay.git
cd relay
git remote add upstream https://github.com/rajputomsingh/relay.git
git switch -c feat/your-change
go mod download
cp .env.example .env
```

Edit `.env` and set `MONGODB_URI` and `RELAY_API_KEY` (minimum 32 characters) for local server development. Never commit `.env` or any file containing credentials.

### MongoDB for integration tests

**Option A — Local MongoDB**

If MongoDB is already running locally:

```bash
export MONGODB_TEST_URI="mongodb://localhost:27017"
go test ./internal/delivery -count=1
```

**Option B — Docker (recommended if you do not have MongoDB installed)**

```bash
docker compose up -d mongodb
MONGODB_TEST_URI="mongodb://localhost:27017" go test ./internal/delivery -count=1
```

On Windows PowerShell, set the environment variable like this:

```powershell
$env:MONGODB_TEST_URI = "mongodb://localhost:27017"
go test ./internal/delivery -count=1
```

### Run the server

After configuring `.env` and ensuring MongoDB is reachable:

```bash
go run ./cmd/relay
```

Verify the liveness endpoint:

```bash
curl http://localhost:8080/healthz
```

## Development Workflow

1. Sync your local `main` branch with upstream:

   ```bash
   git switch main
   git pull upstream main
   ```

2. Create a focused branch:

   ```bash
   git switch -c feat/your-feature
   ```

3. Format Go code:

   ```bash
   gofmt -w cmd internal
   ```

4. Make the smallest coherent change and add or update tests where behavior changes.
5. Run the [validation gate](#validation).
6. Commit your work using [Conventional Commits](https://www.conventionalcommits.org/).
7. Push your branch and open a pull request against `main`.

## Code Guidelines

- Prefer simple, readable, maintainable Go code.
- Handle errors explicitly; do not silently discard failures.
- Validate external input at system boundaries.
- Use context-aware operations for network and database calls.
- Keep concurrency bounded and make lifecycle behavior explicit.
- Avoid unnecessary dependencies.
- Add tests for new behavior and regression fixes.
- Document intentional API or configuration behavior changes.

## Validation

Run the full validation gate before proposing changes:

```bash
go test ./...
go vet ./...
go build ./...
git diff --check
```

Unit tests for configuration, security, and handlers can be run without external infrastructure:

```bash
go test ./internal/config ./internal/security ./internal/handlers -count=1
```

Delivery integration tests require MongoDB:

```bash
export MONGODB_TEST_URI="mongodb://localhost:27017"
go test ./internal/delivery -count=1
```

On Windows PowerShell, use `$env:MONGODB_TEST_URI = "mongodb://localhost:27017"` instead of `export`.

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/) with a clear type and concise description.

Examples:

```text
feat(delivery): add bounded retry scheduling
fix(api): validate event identifiers
test(worker): cover lease recovery
docs: clarify local MongoDB setup
refactor(config): simplify environment validation
```

Common types include `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, and `ci`.

## Branch Naming

Use short, descriptive branch names with a category prefix:

| Prefix      | Use                           |
| ----------- | ----------------------------- |
| `feat/`     | New functionality             |
| `fix/`      | Bug fixes                     |
| `docs/`     | Documentation changes         |
| `test/`     | Test additions or corrections |
| `refactor/` | Internal restructuring        |
| `chore/`    | Maintenance and tooling       |

Examples: `feat/delivery-replay`, `fix/lease-recovery`, `docs/contributor-quickstart`.

## Pull Request Guidelines

Before submitting a pull request:

- Keep changes focused and easy to review.
- Explain what changed and why.
- Link the issue the pull request addresses, where applicable.
- Include relevant test results and reproduction steps for bug fixes.
- Update documentation when behavior or configuration changes.
- Do not commit generated files, local configuration, or secrets.
- Call out breaking changes and operational implications.
- Respond to review feedback with focused follow-up commits.

## What to expect

The following are **maintainer response targets**, not guaranteed service-level commitments. Review time can vary with availability and the size or complexity of a change.

| Stage             | Target                          | What happens                                                        |
| ----------------- | ------------------------------- | ------------------------------------------------------------------- |
| Issue comment     | Within 48 hours                 | Maintainer acknowledges the issue and helps confirm scope           |
| First PR review   | Within 48 hours                 | Initial feedback or approval if the change is ready                 |
| Follow-up reviews | Within 24 hours of your update  | Further review where maintainer availability allows                 |
| Merge             | Within 1 week for small changes | Small, ready-to-merge changes are prioritized                       |
| `CONTRIBUTORS.md` | At merge                        | Your contribution is recognized in the contributor list             |
| Questions         | Within 48 hours                 | Questions in issues, pull requests, or Discussions are acknowledged |

If you do not hear back within those windows, **politely ping the thread**. A reminder is welcome; maintainers may have missed the notification or may be handling other work.

## Architecture Overview

Relay accepts events through its HTTP API, persists them in MongoDB, matches them to enabled endpoints, and schedules independent delivery records for bounded workers.

The delivery engine records attempts and outcomes, retries eligible failures with backoff, and uses expiring leases and fencing to recover work after worker interruption. Delivery is **at least once**, not exactly once. Downstream handlers should therefore be idempotent, using the stable event ID to prevent duplicate side effects.

For a deeper overview, see the [README](README.md) and the architecture documentation in `docs/`.

## Bug Reports

Before opening a bug report:

1. Search existing [issues](https://github.com/rajputomsingh/relay/issues) to avoid duplicates.
2. Include the Relay version, Go version, and relevant environment details.
3. Provide clear reproduction steps and the expected versus actual behavior.
4. Include relevant logs with secrets and sensitive values removed.
5. Mention whether the issue is reproducible with the latest `main` branch.

For suspected security vulnerabilities, do not create a public issue. Use the private reporting route described in the repository's Security policy.

## Security

- Never commit `.env`, API keys, database credentials, signing secrets, or other secrets.
- Do not include sensitive production data in issues, logs, test fixtures, or pull requests.
- Do not publish exploit details for an unpatched vulnerability.
- Report suspected vulnerabilities privately through the repository's GitHub Security reporting feature.

## License

By contributing to Relay, you agree that your contributions are submitted under the project's [Apache License 2.0](LICENSE).
