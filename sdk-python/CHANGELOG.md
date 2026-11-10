# Changelog

All notable changes to `relay-python-sdk` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/).

## [0.2.0] — 2026-11-10

### Breaking changes

- **`Event` dataclass replaced.** `Event` now represents an event *record*
  returned by `GET /v1/events/{id}`, not an event creation request. It has
  fields `id`, `source`, `event_type`, `data`, `idempotency_key`,
  `received_at`, `status`, `delivery_status`, `delivery_attempts`,
  `delivered_at`, `failed_at`, `replay_count`, `last_replayed_at`. The old
  `created_at` field is renamed to `received_at`; the old `status` field
  remains but now reflects the parent lifecycle state, not the ingestion
  response.
- **`Endpoint` dataclass fields expanded.** Fields now include `name`,
  `has_signing_secret`, `max_attempts`, `rate_limit_rps`, `updated_at`,
  `disabled_at`. The `event_types` field is replaced by `event_filter:
  EventFilter` matching the server's actual shape.
- **`Delivery` dataclass field renames.** `attempt_count` → `attempts`;
  `next_retry_at` → `next_attempt_at`; `response_status` → `http_status`
  (moved to attempts). Added `delivered_at`, `failed_at`, `last_error`,
  `updated_at`. `id` and `event_id` fields removed (deliveries are keyed by
  endpoint within an event).
- **`send_event` returns `EventResponse`.** Now returns a typed object with
  `status`, `event_id` (optional), and `idempotency_key`, matching the
  server's actual response. `event_id` is only present on first submission;
  idempotent retries have `status="already_accepted"` and no `event_id`.
- **`send_event` requires `idempotency_key`.** Previously optional; the
  server rejects requests without it, so the SDK now raises `ValueError`
  before sending.
- **`create_endpoint` requires `name`.** The server requires `name` and
  rejects bodies without it (`DisallowUnknownFields`). The old `event_types`
  and `secret` parameters are replaced by `event_filter` and
  `signing_secret`, matching the server's actual field names.
- **`disable_endpoint` returns `Endpoint`.** Previously returned `None`.
  The returned `Endpoint` has only `id` populated because the server's
  DELETE response does not include the full endpoint shape.

### Added

- **`list_events`** — paginated event listing with filter and cursor.
- **`inspect_event`** — `GET /v1/events/{id}`.
- **`list_deliveries`** (renamed from `get_deliveries`).
- **`list_attempts`** (renamed from `get_attempts`); now returns typed
  `AttemptList` instead of raw dict.
- **`health`** and **`ready`** methods.
- **`relay_python.signatures`** module: `sign_webhook_body`,
  `verify_webhook_signature`, `SignatureError`, `DEFAULT_MAX_AGE_MS`.
  Language-native HMAC-SHA256 implementation matching the Relay server's
  signing protocol. Enforces timestamp freshness (default 5 min).

### Changed

- `User-Agent` header bumped to `relay-python/0.2.0`.
- `Endpoint.event_filter` is now an `EventFilter` dataclass rather than a
  raw list of event types.

### Fixed

- `Endpoint.from_dict` no longer reads a nonexistent `event_types` field.
- `Delivery.from_dict` no longer reads nonexistent `attempt_count` or
  `next_retry_at` fields.

## [0.1.0] — initial release

- Initial event submission and endpoint management.
- Never worked correctly against the Relay server (used mock-only tests).
  Do not use. Upgrade to 0.2.0.