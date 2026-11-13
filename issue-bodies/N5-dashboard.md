## Problem

The endpoint-health API is currently easiest to inspect with curl. A minimal browser dashboard would make endpoint status visible without requiring command-line tooling.

## Scope

Add a single-page dashboard served at GET /dashboard that:

- Prompts for the Relay API key and stores it in localStorage.
- Lists endpoints using the existing endpoint-list API.
- Polls each endpoint's health every 10 seconds.
- Displays success rate, attempts, consecutive failures, and latest timestamps.
- Shows network and authorization errors inline rather than swallowing them.
- Uses clear status states based on documented health response fields.

Keep it to one HTML file with inline JavaScript and CSS; no framework or build step.

## Acceptance Criteria

- [ ] Served at GET /dashboard.
- [ ] Endpoint table includes readable status indicators.
- [ ] 401 and network errors are shown inline.
- [ ] Works against a local Relay instance.
- [ ] No external JavaScript or CSS dependencies.
- [ ] Polling pauses when the browser tab is hidden, if practical.

## Hints

Consider //go:embed for serving a single HTML file. Confirm the actual health response shape in the OpenAPI document before implementing the UI.

**Size:** S (~3 hours)
