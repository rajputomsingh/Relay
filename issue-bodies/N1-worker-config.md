## Bug

Starting Relay with only MONGODB_URI, MONGODB_DATABASE, and RELAY_API_KEY configured can produce:

``text
delivery worker configuration invalid: poll interval must be positive
``

The delivery worker then does not start, preventing local event delivery.

## Investigation

Inspect how internal/config/config.go parses the default for RELAY_WORKER_POLL_SECONDS. Trace the string-to-integer or duration conversion before assuming the root cause.

## Fix

- Ensure an unset RELAY_WORKER_POLL_SECONDS produces a valid positive poll interval.
- Add a config test for the default path.
- Check other integer environment variables that share the same parsing path.

## Acceptance Criteria

- [ ] Relay starts with only the required environment variables and the worker starts polling.
- [ ] internal/config/config_test.go covers the unset default.
- [ ] Setting RELAY_WORKER_POLL_SECONDS=5 continues to work.
- [ ] Related integer environment variables are checked for the same class of bug.

## Verification

``powershell
Remove-Item Env:RELAY_WORKER_POLL_SECONDS -ErrorAction SilentlyContinue
go run ./cmd/relay
``

Confirm there is no invalid-poll-interval error and that the worker reports polling has started.

**Size:** S (~1 hour)
