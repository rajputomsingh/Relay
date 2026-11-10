"""Data models for the Relay SDK.

Field names and nullability mirror the Go structs in internal/handlers/.
Do not rename these fields; SDK consumers depend on the exact keys.
"""

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any


def _parse_dt(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


# ---------- Events ----------


@dataclass(frozen=True)
class EventResponse:
    """Response from POST /v1/events.

    On first submission: status="accepted", event_id is set.
    On idempotent retry: status="already_accepted", event_id is empty.
    Callers must check status before using event_id.
    """

    status: str
    event_id: str | None
    idempotency_key: str

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "EventResponse":
        return cls(
            status=d["status"],
            event_id=d.get("event_id"),
            idempotency_key=d.get("idempotency_key", ""),
        )


@dataclass(frozen=True)
class Event:
    """An event as returned by GET /v1/events/{id}.

    This is the parent event record. Its delivery_status is derived by the
    server's lifecycle aggregator from the child delivery states.
    """

    id: str
    source: str
    event_type: str
    data: dict[str, Any]
    idempotency_key: str
    received_at: datetime
    status: str
    delivery_status: str
    delivery_attempts: int
    delivered_at: datetime | None = None
    failed_at: datetime | None = None
    replay_count: int = 0
    last_replayed_at: datetime | None = None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Event":
        return cls(
            id=d["id"],
            source=d["source"],
            event_type=d["event_type"],
            data=d.get("data", {}),
            idempotency_key=d.get("idempotency_key", ""),
            received_at=_parse_dt(d["received_at"]) or datetime.now(),
            status=d.get("status", ""),
            delivery_status=d.get("delivery_status", ""),
            delivery_attempts=int(d.get("delivery_attempts", 0)),
            delivered_at=_parse_dt(d.get("delivered_at")),
            failed_at=_parse_dt(d.get("failed_at")),
            replay_count=int(d.get("replay_count", 0)),
            last_replayed_at=_parse_dt(d.get("last_replayed_at")),
        )


@dataclass(frozen=True)
class EventList:
    """Response from GET /v1/events."""

    events: list[Event]
    limit: int
    has_more: bool
    next_cursor: str | None = None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "EventList":
        return cls(
            events=[Event.from_dict(e) for e in d.get("events", [])],
            limit=int(d.get("limit", 0)),
            has_more=bool(d.get("has_more", False)),
            next_cursor=d.get("next_cursor"),
        )


@dataclass(frozen=True)
class ReplayResponse:
    """Response from POST /v1/events/{id}/replay."""

    status: str
    event_id: str
    idempotency_key: str
    deliveries_requeued: int

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "ReplayResponse":
        return cls(
            status=d["status"],
            event_id=d["event_id"],
            idempotency_key=d.get("idempotency_key", ""),
            deliveries_requeued=int(d.get("deliveries_requeued", 0)),
        )


# ---------- Deliveries ----------


@dataclass(frozen=True)
class Delivery:
    """A single delivery record for one (event, endpoint) pair."""

    endpoint_id: str
    status: str
    attempts: int
    max_attempts: int
    next_attempt_at: datetime | None = None
    delivered_at: datetime | None = None
    failed_at: datetime | None = None
    last_error: str = ""
    updated_at: datetime | None = None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Delivery":
        return cls(
            endpoint_id=d["endpoint_id"],
            status=d["status"],
            attempts=int(d.get("attempts", 0)),
            max_attempts=int(d.get("max_attempts", 0)),
            next_attempt_at=_parse_dt(d.get("next_attempt_at")),
            delivered_at=_parse_dt(d.get("delivered_at")),
            failed_at=_parse_dt(d.get("failed_at")),
            last_error=d.get("last_error", ""),
            updated_at=_parse_dt(d.get("updated_at")),
        )


# ---------- Attempts ----------


@dataclass(frozen=True)
class Attempt:
    """One recorded delivery attempt for an event/endpoint pair."""

    id: str
    event_id: str
    endpoint_id: str
    attempt_number: int
    started_at: datetime
    finished_at: datetime
    duration_ms: int
    http_status: int | None
    outcome: str
    replay_cycle: int = 0
    error: str = ""

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Attempt":
        return cls(
            id=d["id"],
            event_id=d["event_id"],
            endpoint_id=d.get("endpoint_id", ""),
            attempt_number=int(d["attempt_number"]),
            started_at=_parse_dt(d["started_at"]) or datetime.now(),
            finished_at=_parse_dt(d["finished_at"]) or datetime.now(),
            duration_ms=int(d.get("duration_ms", 0)),
            http_status=d.get("http_status"),
            outcome=d.get("outcome", ""),
            replay_cycle=int(d.get("replay_cycle", 0)),
            error=d.get("error", ""),
        )


@dataclass(frozen=True)
class AttemptList:
    """Response from GET /v1/events/{id}/attempts."""

    event_id: str
    attempts: list[Attempt]
    limit: int
    has_more: bool
    next_cursor: str | None = None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "AttemptList":
        return cls(
            event_id=d.get("event_id", ""),
            attempts=[Attempt.from_dict(a) for a in d.get("attempts", [])],
            limit=int(d.get("limit", 0)),
            has_more=bool(d.get("has_more", False)),
            next_cursor=d.get("next_cursor"),
        )


# ---------- Endpoints ----------


@dataclass(frozen=True)
class EventFilter:
    """Restricts which events an endpoint receives.

    Empty filter matches all events.
    """

    sources: list[str] = field(default_factory=list)
    event_types: list[str] = field(default_factory=list)

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "EventFilter":
        if not d:
            return cls()
        return cls(
            sources=list(d.get("sources", [])),
            event_types=list(d.get("event_types", [])),
        )

    def to_dict(self) -> dict[str, list[str]]:
        result: dict[str, list[str]] = {}
        if self.sources:
            result["sources"] = self.sources
        if self.event_types:
            result["event_types"] = self.event_types
        return result


@dataclass(frozen=True)
class Endpoint:
    """A downstream webhook destination."""

    id: str
    name: str
    url: str
    has_signing_secret: bool
    enabled: bool
    max_attempts: int
    event_filter: EventFilter
    rate_limit_rps: int = 0
    created_at: datetime | None = None
    updated_at: datetime | None = None
    disabled_at: datetime | None = None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Endpoint":
        return cls(
            id=d["id"],
            name=d.get("name", ""),
            url=d["url"],
            has_signing_secret=bool(d.get("has_signing_secret", False)),
            enabled=bool(d.get("enabled", True)),
            max_attempts=int(d.get("max_attempts", 0)),
            event_filter=EventFilter.from_dict(d.get("event_filter")),
            rate_limit_rps=int(d.get("rate_limit_rps", 0)),
            created_at=_parse_dt(d.get("created_at")),
            updated_at=_parse_dt(d.get("updated_at")),
            disabled_at=_parse_dt(d.get("disabled_at")),
        )


@dataclass(frozen=True)
class EndpointList:
    """Response from GET /v1/endpoints."""

    endpoints: list[Endpoint]
    count: int

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "EndpointList":
        return cls(
            endpoints=[Endpoint.from_dict(e) for e in d.get("endpoints", [])],
            count=int(d.get("count", 0)),
        )


# ---------- Health ----------


@dataclass(frozen=True)
class HealthResponse:
    """Response from GET /healthz or GET /readyz."""

    status: str
    service: str = ""
    database: str = ""

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "HealthResponse":
        return cls(
            status=d.get("status", ""),
            service=d.get("service", ""),
            database=d.get("database", ""),
        )
