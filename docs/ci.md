# CI and required checks

## Required checks

Every pull request to `main` must pass these three checks:

- **Build**: compiles the whole Go project with `go build ./...`.
- **Go (lint, vet, test)**: checks formatting, runs `go vet` and the linter, and runs the tests with MongoDB.
- **Node SDK**: installs, typechecks, builds and tests the Node SDK.

## Where they are set

The required checks are set in one place only: the `main-protection` ruleset (Settings → Rules → Rulesets). Classic branch protection does not list them, so there is a single source of truth.

## If you rename a job

The ruleset matches each check by the job's `name:` in `.github/workflows/ci.yml`, exactly. If you rename a job, update the ruleset in the same change. Otherwise pull requests get stuck on "Expected — Waiting for status to be reported", even when every check has passed.

## Docker build

The Docker build only runs on pushes to `main`, not on pull requests. This is on purpose, so it is not a required check.