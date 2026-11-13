## Problem

The Node.js SDK currently exposes a common RelayAPIError for failed requests. Consumers often need to branch on HTTP status to handle common cases.

## Scope

Add typed subclasses while preserving RelayAPIError as the base class:

``typescript
export class BadRequestError extends RelayAPIError {}   // 400
export class UnauthorizedError extends RelayAPIError {} // 401
export class NotFoundError extends RelayAPIError {}     // 404
export class RateLimitedError extends RelayAPIError {}  // 429
export class ServerError extends RelayAPIError {}       // 5xx
``

Map response statuses to the appropriate subclass in the SDK request helper.

## Acceptance Criteria

- [ ] Subclasses are exported from sdk-node/src/errors.ts and re-exported from sdk-node/src/index.ts.
- [ ] The request helper throws the correct subclass for each supported status.
- [ ] instanceof RelayAPIError continues to catch every subclass.
- [ ] Tests cover each status mapping.
- [ ] The SDK README documents the error classes.
- [ ] Existing tests continue to pass.

## Contributor Notes

Inspect the current request helper and error constructor before implementation. Preserve the existing status and response-body information on the base error.

**Size:** S (~1.5 hours)
