# relay-issues.ps1
# Updates Relay issues #69, #72, #76, #77; creates seven issues;
# then rewrites and comments on #81 using the actual issue numbers.
# Run from the Relay repository root. Requires GitHub CLI (gh) and repo write access.

$ErrorActionPreference = "Stop"
$Repo = "rajputomsingh/Relay"
$Bodies = Join-Path $PWD "issue-bodies"
$Utf8NoBom = New-Object System.Text.UTF8Encoding($false)

function Write-Body {
    param([string]$Name, [string]$Content)
    [System.IO.File]::WriteAllText((Join-Path $Bodies $Name), $Content, $Utf8NoBom)
}

function Invoke-Gh {
    param([string[]]$GhArgs)
    $output = & gh @GhArgs
    if ($LASTEXITCODE -ne 0) {
        throw "GitHub CLI failed: gh $($GhArgs -join ' ')"
    }
    return ($output | Out-String).Trim()
}

function Update-Issue {
    param([int]$Number, [string]$BodyFile)
    Invoke-Gh @("issue", "edit", "$Number", "--repo", $Repo, "--body-file", (Join-Path $Bodies $BodyFile)) | Out-Null
    Write-Host "Updated #$Number" -ForegroundColor Green
}

function New-Issue {
    param([string]$Title, [string]$Labels, [string]$BodyFile)

    # Safe to rerun: reuse an existing issue with the exact same title.
    $existingJson = Invoke-Gh @("issue", "list", "--repo", $Repo, "--state", "all", "--limit", "100", "--json", "number,title")
    $existing = @($existingJson | ConvertFrom-Json | Where-Object { $_.title -ceq $Title })
    if ($existing.Count -gt 0) {
        $number = [int]$existing[0].number
        Write-Host "Reusing existing issue #$number — $Title" -ForegroundColor Yellow
        return $number
    }

    $url = Invoke-Gh @(
        "issue", "create",
        "--repo", $Repo,
        "--title", $Title,
        "--label", $Labels,
        "--body-file", (Join-Path $Bodies $BodyFile)
    )
    if ($url -notmatch '/issues/(\d+)$') {
        throw "Could not parse issue number from GitHub response: $url"
    }
    $number = [int]$Matches[1]
    Write-Host "Created #$number — $Title" -ForegroundColor Green
    return $number
}

function Render-Template {
    param([string]$Name, [hashtable]$Numbers)
    $content = [System.IO.File]::ReadAllText((Join-Path $Bodies $Name))
    foreach ($key in $Numbers.Keys) {
        $content = $content.Replace("__$key__", [string]$Numbers[$key])
    }
    return $content
}

# Preflight
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    throw "GitHub CLI (gh) is not installed or is not on PATH."
}
if (-not (Test-Path (Join-Path $PWD "go.mod"))) {
    Write-Warning "Current directory does not look like the Relay repository root (go.mod not found)."
}
Invoke-Gh @("auth", "status", "--hostname", "github.com") | Out-Null
$repoInfo = Invoke-Gh @("repo", "view", $Repo, "--json", "nameWithOwner", "--jq", ".nameWithOwner")
if ($repoInfo -ne $Repo) { throw "Unexpected repository: $repoInfo" }

New-Item -ItemType Directory -Force -Path $Bodies | Out-Null
Write-Host "Writing Markdown issue bodies to $Bodies" -ForegroundColor Cyan

# Existing issue #69
Write-Body "69.md" @'
## Summary

Relay's handlers return client-facing error strings, but there is no central reference listing each error value, its HTTP status, when it occurs, and the action a client should take.

## Scope

1. Enumerate every client-facing error emitted by `internal/handlers/`.
2. Centralize those values as exported constants in `internal/handlers/errors.go`.
3. Replace inline error strings in handlers with the constants.
4. Add `docs/api-errors.md` with a table containing HTTP status, error value, trigger, and recommended client action.
5. Add `internal/handlers/errors_test.go` to prevent handler error strings from drifting away from the constants.
6. Link the new reference from `README.md`.
7. Update the corresponding OpenAPI error responses and regenerate generated documentation where applicable.

## Acceptance Criteria

- [ ] `errors.go` contains every client-facing error string emitted by `internal/handlers/`.
- [ ] Handlers use the constants rather than repeating inline values.
- [ ] `docs/api-errors.md` matches the actual implementation.
- [ ] A test prevents future drift.
- [ ] README links to the error reference.
- [ ] OpenAPI documents the corresponding error responses per endpoint.

## Expected Files

- `internal/handlers/errors.go`
- `internal/handlers/errors_test.go`
- `internal/handlers/*.go`
- `docs/api-errors.md`
- OpenAPI source and generated documentation
- `README.md`

## Validation

```bash
go test ./internal/handlers/ -run Error
go vet ./...
```

## Contributor Notes

Do not invent error values, status mappings, or retry behavior. Derive every documented value from the actual handler source. Exclude internal-only errors that are never returned to clients and explain the exclusion in a comment.

**Size:** S–M
'@

# Existing issue #72
Write-Body "72.md" @'
## Summary

Expand the official Node.js SDK documentation with practical examples for endpoint management, event replay, delivery inspection, and endpoint health.

The SDK README currently focuses on event submission. Add examples for the operations supported by the current implementation.

## Scope

Before documenting each operation, verify its method name, signature, and return type against `sdk-node/src/index.ts` and `sdk-node/src/types.ts`.

Cover:

- List endpoints
- Create an endpoint
- Update an endpoint
- Get endpoint health with `client.getEndpointHealth(id)`, including nullable timestamps
- Replay an event
- Inspect event deliveries

## Acceptance Criteria

- [ ] One copy-pasteable example per operation.
- [ ] Each example is approximately 3–8 lines.
- [ ] Examples use realistic request parameters.
- [ ] At least one example demonstrates handling a 401, 404, or 503 response.
- [ ] Client initialization appears once; later examples reuse the client.
- [ ] Return types match the SDK source.
- [ ] The endpoint-health example shows the response shape, including `null` fields.
- [ ] Link to the API reference for further detail.

## Expected Files

- `sdk-node/README.md`

## Validation

Run the documented snippets against a local Relay instance:

```bash
go run ./cmd/relay
```

Ensure `RELAY_API_KEY` is configured with at least 32 characters.

## Contributor Notes

Keep examples copy-pasteable. Do not document methods, parameters, or return values the current SDK does not support. If a method is missing, open a separate issue rather than inventing it.

**Size:** S (2–3 hours)
'@

# Existing issue #76
Write-Body "76.md" @'
## Context

Relay currently assumes a single tenant per deployment: endpoints, events, and deliveries share one database without a tenant dimension. This issue is a design RFC, not an implementation.

## Deliverable

Write `docs/rfcs/0001-multi-tenant.md` covering:

1. **Isolation model** — compare shared collections with `tenant_id` and compound indexes, database-per-tenant, collection-per-tenant, and hybrid approaches. Recommend and justify one.
2. **Authentication** — explain API-key-to-tenant mapping, JWT claims, and whether tenant identity belongs in a path, header, or API key.
3. **Migration** — explain how an existing single-tenant deployment becomes tenant-0 and whether a migration script is required.
4. **Query impact** — identify queries that need tenant filters and those affected by index changes.
5. **Index strategy** — specify compound indexes required by the chosen model.
6. **Operations** — cover per-tenant backup/restore, deletion, export, and rate limits.
7. **Test strategy** — demonstrate how tests prove tenant A cannot access tenant B.
8. **SDK compatibility** — describe changes required in `sdk-node` and `sdk-go`.
9. **Alternatives** — include the option of deploying one Relay instance per tenant.

## Acceptance Criteria

- [ ] RFC exists at `docs/rfcs/0001-multi-tenant.md`.
- [ ] All nine sections are covered.
- [ ] A documentation PR is opened against `main`.
- [ ] The RFC receives substantive maintainer review.
- [ ] The RFC is revised after review.
- [ ] Implementation issues are opened from the accepted RFC and linked here.

## Contributor Notes

If two isolation models remain plausible, compare their trade-offs rather than choosing prematurely. This RFC should establish the design before implementation begins.

**Size:** L (design)
'@

# Existing issue #77
Write-Body "77.md" @'
## Context

Relay's delivery worker runs concurrent goroutines against the `deliveries` collection. Lease acquisition also writes to `idx_delivery_due`. There is no benchmark quantifying contention, so the optimal worker concurrency and index shape are not yet established.

## What to Measure

Build a benchmark that:

1. Seeds a MongoDB test database with 10,000 deliveries in `pending` state across 100 endpoints.
2. Runs the worker loop at concurrency levels 1, 2, 4, 8, and 16.
3. Reports, for each level:
   - Deliveries per second
   - P50, P95, and P99 delivery latency
   - MongoDB `db.serverStatus().opcounters` deltas for queries, inserts, and updates

## Deliverables

- [ ] Go harness in `bench/mongo-contention/`, runnable with `go run ./bench/mongo-contention`.
- [ ] Cleanly skips when `MONGODB_TEST_URI` is unset.
- [ ] Results documented in `docs/benchmarks/mongo-contention.md` with a table and short narrative.
- [ ] Recommendations split by load tier: below 100/s, 100–1,000/s, and above 1,000/s.
- [ ] Worker-concurrency, index, and batching recommendations include rationale.
- [ ] A conclusion comment is posted on this issue.

## Constraints

- Do not change production code; this is an investigation.
- Test against a local `mongo:7` container. Do not use shared infrastructure.
- If contention is in the worker lease-poll loop rather than MongoDB, document that finding.

## Hints

- `db.serverStatus().opcounters` exposes server-side operation counters.
- Call `b.ResetTimer()` after seeding so setup is excluded.
- A local MongoDB development container on port 27017 may be used if it is isolated and safe for this test.

**Size:** M (half day)
'@

# New issue 1
Write-Body "N1-worker-config.md" @'
## Bug

Starting Relay with only `MONGODB_URI`, `MONGODB_DATABASE`, and `RELAY_API_KEY` configured can produce:

```text
delivery worker configuration invalid: poll interval must be positive
```

The delivery worker then does not start, so events are not delivered locally.

## Investigation

Inspect how `internal/config/config.go` parses the default for `RELAY_WORKER_POLL_SECONDS`, currently expected to be a positive value. Trace the string-to-integer or duration conversion before assuming the root cause.

## Fix

- Ensure an unset `RELAY_WORKER_POLL_SECONDS` produces a valid positive poll interval.
- Add a config test for the default path.
- Check other integer environment variables that share the same parsing path.

## Acceptance Criteria

- [ ] Relay starts with only the required environment variables and the worker starts polling.
- [ ] `internal/config/config_test.go` covers the unset default.
- [ ] Setting `RELAY_WORKER_POLL_SECONDS=5` continues to work.
- [ ] Related integer environment variables are checked for the same class of bug.

## Verification

```powershell
Remove-Item Env:RELAY_WORKER_POLL_SECONDS -ErrorAction SilentlyContinue
go run ./cmd/relay
```

Confirm there is no invalid-poll-interval error and that the worker reports that polling has started.

**Size:** S (~1 hour)
'@

# New issue 2
Write-Body "N2-dotenv.md" @'
## Problem

Contributors currently need to configure `MONGODB_URI`, `MONGODB_DATABASE`, and `RELAY_API_KEY` in their shell before running Relay. Missing values lead to avoidable startup errors.

## Scope

Add local `.env` support:

1. Load `.env` from the current working directory when the file exists.
2. Do not override variables already set in the process environment.
3. Return a clear error if an existing `.env` file has invalid syntax.
4. Maintain `.env.example` with every variable used by `internal/config/config.go`, including requiredness and the 32-character API-key requirement.
5. Ensure `.env` is ignored by Git.
6. Document the setup in `CONTRIBUTING.md`.

## Acceptance Criteria

- [ ] Copying `.env.example` to `.env` allows local startup without manually exporting each variable.
- [ ] Explicit process environment variables take precedence over `.env`.
- [ ] `.env` is not committed.
- [ ] `.env.example` lists every configuration variable.
- [ ] `CONTRIBUTING.md` documents the workflow.
- [ ] A test verifies that the process environment wins over `.env`.

## Hints

`github.com/joho/godotenv` is an option. A small standard-library loader is also acceptable. Check the existing `.gitignore` before changing it.

**Size:** S (~2 hours)
'@

# New issue 3
Write-Body "N3-sdk-errors.md" @'
## Problem

The Node.js SDK currently exposes a common `RelayAPIError` for failed requests. Consumers often need to branch on HTTP status to handle common cases.

## Scope

Consider adding typed subclasses while preserving `RelayAPIError` as the base class:

```ts
export class BadRequestError extends RelayAPIError {}   // 400
export class UnauthorizedError extends RelayAPIError {} // 401
export class NotFoundError extends RelayAPIError {}     // 404
export class RateLimitedError extends RelayAPIError {}  // 429
export class ServerError extends RelayAPIError {}       // 5xx
```

Map response statuses to the appropriate subclass in the SDK request helper.

## Acceptance Criteria

- [ ] Subclasses are exported from `sdk-node/src/errors.ts` and re-exported from `sdk-node/src/index.ts`.
- [ ] The request helper throws the correct subclass for each supported status.
- [ ] `instanceof RelayAPIError` continues to catch every subclass.
- [ ] Tests cover each status mapping.
- [ ] The SDK README documents the error classes.
- [ ] Existing tests continue to pass.

## Contributor Notes

Inspect the current request helper and error constructor before implementation. Preserve the existing status and response-body information on the base error.

**Size:** S (~1.5 hours)
'@

# New issue 4
Write-Body "N4-service-health.md" @'
## Context

Per-endpoint health (`GET /v1/endpoints/{id}/health`) reports the health of a webhook destination. It does not report whether the Relay service itself is healthy. Service-level monitoring and orchestration need a separate endpoint.

## Scope

Add an unauthenticated `GET /v1/health` endpoint. A possible response shape is:

```json
{
  "status": "ok",
  "mongo": "ok",
  "worker": "ok",
  "version": "dev",
  "uptime_seconds": 3612
}
```

Define and document the exact status semantics from observable service state:

- `status`: `ok`, `degraded`, or `down`
- `mongo`: `ok` or `unreachable`, using a bounded ping timeout
- `worker`: `ok`, `stale`, or `disabled`, based on actual worker state
- `version`: build version, with a sensible development default
- `uptime_seconds`: elapsed time since process start

Return HTTP 200 for `ok` or `degraded`, and 503 for `down`, if that mapping is supported by the final design.

## Acceptance Criteria

- [ ] Endpoint is served without authentication.
- [ ] MongoDB check has a hard timeout.
- [ ] Worker state reflects actual state, including when the worker never started.
- [ ] OpenAPI is updated.
- [ ] Integration tests cover healthy service, unreachable MongoDB, and worker-not-started states.
- [ ] README explains how to use the endpoint for service monitoring.

## Hints

Reuse the existing MongoDB ping pattern where appropriate. Track the worker's last successful poll if that is the most reliable signal.

**Size:** M (half day)
'@

# New issue 5
Write-Body "N5-dashboard.md" @'
## Problem

The endpoint-health API is currently easiest to inspect with `curl`. A minimal browser dashboard would make endpoint status visible without requiring command-line tooling.

## Scope

Add a single-page dashboard served at `GET /dashboard` that:

- Prompts for the Relay API key and stores it in `localStorage`.
- Lists endpoints using the existing endpoint-list API.
- Polls each endpoint's health every 10 seconds.
- Displays success rate, attempts, consecutive failures, and latest timestamps.
- Shows network and authorization errors inline rather than swallowing them.
- Uses clear status states based on documented health response fields.

Keep it to one HTML file with inline JavaScript and CSS; no framework or build step.

## Acceptance Criteria

- [ ] Served at `GET /dashboard`.
- [ ] Endpoint table includes readable status indicators.
- [ ] 401 and network errors are shown inline.
- [ ] Works against a local Relay instance.
- [ ] No external JavaScript or CSS dependencies.
- [ ] Polling pauses when the browser tab is hidden, if practical.

## Hints

Consider `//go:embed` for serving a single HTML file. Confirm the actual health response shape in the OpenAPI document before implementing the UI.

**Size:** S (~3 hours)
'@

# New issue 6
Write-Body "N6-latency.md" @'
## Context

The endpoint-health response reports attempt counts, success rate, and consecutive failures, but does not report delivery latency. Delivery attempts record start and finish timestamps, which may support latency calculations.

## Scope

Add these additive fields to endpoint health:

```json
{
  "p50_latency_ms": 42,
  "p95_latency_ms": 187,
  "p99_latency_ms": 512
}
```

Calculate percentiles over the same one-hour window used for `attempts_1h`, using successful attempts (`outcome = "delivered"`) only. Return `null` when there are no successful attempts in the window.

## Acceptance Criteria

- [ ] All three fields are present in the response.
- [ ] Fields are `null` when no successful attempts exist in the window.
- [ ] Calculation is integrated into the existing aggregation where feasible; avoid unnecessary database round trips.
- [ ] Integration tests seed known latencies and assert the percentile behavior.
- [ ] OpenAPI marks the fields nullable.
- [ ] Node SDK types are updated.
- [ ] PR description includes a response example.

## Hints

MongoDB 7.0+ provides `$percentile`. Confirm the deployed MongoDB version and aggregation semantics before relying on it. Compute latency from `finished_at - started_at` in milliseconds and filter to delivered outcomes.

Keep the existing response fields unchanged; these additions must be backward-compatible.

**Size:** M (half day)
'@

# New issue 7
Write-Body "N7-go-sdk.md" @'
## Context

The Node.js SDK supports `getEndpointHealth(id)`, but the Go SDK does not currently expose an equivalent operation.

## Scope

Add a Go SDK method:

```go
func (c *Client) GetEndpointHealth(ctx context.Context, endpointID string) (*EndpointHealth, error)
```

Add an `EndpointHealth` type that mirrors the actual API response. Use pointer fields for nullable timestamps where appropriate.

## Acceptance Criteria

- [ ] Method is added to the Go SDK package.
- [ ] `EndpointHealth` mirrors the OpenAPI response.
- [ ] `httptest.Server` tests cover success, 404, and 401.
- [ ] Method has a Go documentation comment.
- [ ] `sdk-go/README.md` includes an example.

## Contributor Notes

Follow the existing Go SDK patterns for requests and non-2xx responses. Use the OpenAPI document as the source of truth for response fields.

**Size:** S (~2 hours)
'@

# Contributor index template
Write-Body "81.md" @'
## Start here

Relay is an open-source webhook delivery service built with Go and MongoDB. First-time contributors are welcome.

Every issue below has a defined scope and acceptance criteria. Before starting, comment on the issue and wait for maintainer confirmation.

## Code — good first issues

| Issue | What | Area | Size |
|---|---|---|---|
| [#__N1__](https://github.com/rajputomsingh/Relay/issues/__N1__) | Worker config default bug | Go | S |
| [#__N2__](https://github.com/rajputomsingh/Relay/issues/__N2__) | `.env` support for local development | Go | S |
| [#__N3__](https://github.com/rajputomsingh/Relay/issues/__N3__) | Typed Node.js SDK errors | TypeScript | S |
| [#__N4__](https://github.com/rajputomsingh/Relay/issues/__N4__) | Service-level `GET /v1/health` | Go | M |
| [#__N5__](https://github.com/rajputomsingh/Relay/issues/__N5__) | Health dashboard at `/dashboard` | HTML/JS | S |

## Docs — good first issues

| Issue | What |
|---|---|
| [#69](https://github.com/rajputomsingh/Relay/issues/69) | Enumerate API error codes |
| [#68](https://github.com/rajputomsingh/Relay/issues/68) | Python client usage example |
| [#71](https://github.com/rajputomsingh/Relay/issues/71) | Python HMAC webhook verification |
| [#72](https://github.com/rajputomsingh/Relay/issues/72) | Expand Node.js SDK examples |

## Help wanted — smaller code changes

| Issue | What |
|---|---|
| [#74](https://github.com/rajputomsingh/Relay/issues/74) | Worker-crash recovery integration test |
| [#__N7__](https://github.com/rajputomsingh/Relay/issues/__N7__) | Go SDK `GetEndpointHealth` |

## Advanced / design

| Issue | What |
|---|---|
| [#76](https://github.com/rajputomsingh/Relay/issues/76) | Multi-tenant isolation RFC |
| [#77](https://github.com/rajputomsingh/Relay/issues/77) | MongoDB contention benchmark |
| [#__N6__](https://github.com/rajputomsingh/Relay/issues/__N6__) | Endpoint-health latency percentiles |

## How to claim an issue

1. Read the issue and check whether another contributor has claimed it.
2. Comment that you would like to work on it.
3. Wait for maintainer assignment before substantial implementation.
4. Read [CONTRIBUTING.md](https://github.com/rajputomsingh/Relay/blob/main/CONTRIBUTING.md).
5. Open a draft PR early for feedback.
6. Reference the issue in the PR body with `Closes #<number>`.

## What contributors can expect

- Maintainer review and feedback.
- Credit for merged contributions in project documentation or release notes, where applicable.
- Guidance on setup and implementation questions.

## Not sure where to start?

Comment with your experience level, preferred area (Go, TypeScript, docs, or infrastructure), and approximate availability. The maintainers can help identify a suitable issue.

Repository: https://github.com/rajputomsingh/Relay
'@

# Comment template
Write-Body "81-comment.md" @'
A new batch of scoped contributor issues is available:

### Smaller starting points

- **#__N1__ — Worker configuration default.** Investigate and fix the invalid default poll interval that can prevent the delivery worker from starting.
- **#__N2__ — `.env` support.** Improve local setup while preserving process-environment precedence.
- **#__N3__ — Typed Node.js SDK errors.** Add status-specific error subclasses while preserving the base error type.

### Feature work

- **#__N4__ — Service-level health endpoint.** Add a bounded service-health check suitable for monitoring.
- **#__N5__ — Health dashboard.** Build a minimal browser view for endpoint health and delivery status.

### Advanced work

- **#__N6__ — Latency percentiles.** Extend endpoint health with p50, p95, and p99 successful-delivery latency.
- **#__N7__ — Go SDK endpoint health.** Add the Go equivalent of the Node SDK endpoint-health method.

Please read the full issue before starting and comment to coordinate ownership. If you are new to OSS, the worker configuration or `.env` issue is a reasonable place to begin.
'@

Write-Host "`nAll Markdown bodies have been written." -ForegroundColor Cyan
Write-Host "Review them in: $Bodies" -ForegroundColor Cyan

$answer = Read-Host "Proceed with GitHub changes (4 edits, 7 issue creates/reuses, #81 update + comment)? Type YES"
if ($answer -cne "YES") {
    Write-Host "Cancelled. No GitHub issues were changed." -ForegroundColor Yellow
    return
}

# Update existing issues
Write-Host "`nUpdating existing issues..." -ForegroundColor Cyan
Update-Issue 69 "69.md"
Update-Issue 72 "72.md"
Update-Issue 76 "76.md"
Update-Issue 77 "77.md"

# Create or reuse the seven new issues; capture their real issue numbers.
Write-Host "`nCreating or reusing the seven new issues..." -ForegroundColor Cyan
$N1 = New-Issue "fix(worker): correct the default delivery worker poll interval" "bug,good first issue" "N1-worker-config.md"
$N2 = New-Issue "dx: support .env file for local development" "help wanted,good first issue" "N2-dotenv.md"
$N3 = New-Issue "feat(sdk-node): add typed error subclasses for common HTTP statuses" "enhancement,sdk,good first issue" "N3-sdk-errors.md"
$N4 = New-Issue "feat(ops): add service-level GET /v1/health endpoint" "enhancement,good first issue" "N4-service-health.md"
$N5 = New-Issue "feat(ui): add a minimal endpoint health dashboard" "enhancement,good first issue" "N5-dashboard.md"
$N6 = New-Issue "feat(health): add p50/p95/p99 delivery latency to endpoint health" "enhancement,api,sdk,advanced" "N6-latency.md"
$N7 = New-Issue "feat(sdk-go): add GetEndpointHealth to the Go SDK" "enhancement,sdk,help wanted" "N7-go-sdk.md"

$numbers = @{
    N1 = $N1; N2 = $N2; N3 = $N3; N4 = $N4
    N5 = $N5; N6 = $N6; N7 = $N7
}

# Render #81 using actual issue numbers (never assume sequential numbering).
$body81 = Render-Template "81.md" $numbers
$final81 = Join-Path $Bodies "81-final.md"
[System.IO.File]::WriteAllText($final81, $body81, $Utf8NoBom)
Invoke-Gh @("issue", "edit", "81", "--repo", $Repo, "--body-file", $final81) | Out-Null
Write-Host "Updated contributor index #81" -ForegroundColor Green

$comment81 = Render-Template "81-comment.md" $numbers
$finalComment = Join-Path $Bodies "81-comment-final.md"
[System.IO.File]::WriteAllText($finalComment, $comment81, $Utf8NoBom)
Invoke-Gh @("issue", "comment", "81", "--repo", $Repo, "--body-file", $finalComment) | Out-Null
Write-Host "Posted contributor-batch comment on #81" -ForegroundColor Green

Write-Host "`nCreated/reused issue numbers:" -ForegroundColor Cyan
Write-Host "N1=$N1  N2=$N2  N3=$N3  N4=$N4  N5=$N5  N6=$N6  N7=$N7"

Write-Host "`nFinal open issue list:" -ForegroundColor Cyan
Invoke-Gh @(
    "issue", "list", "--repo", $Repo, "--state", "open", "--limit", "100",
    "--json", "number,title,labels",
    "--template", '{{range .}}#{{.number}} {{.title}}{{range .labels}} [{{.name}}]{{end}}{{"\n"}}{{end}}'
)
