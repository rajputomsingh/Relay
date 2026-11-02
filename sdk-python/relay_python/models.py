"""Data models for the Relay SDK."""
from dataclasses import dataclass
from datetime import datetime
from typing import Any


def _parse_dt(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


@dataclass(frozen=True)
class Event:
    """A webhook event submitted to Relay."""

    id: str
    source: str
    event_type: str
    data: dict[str, Any]
    status: str
    created_at: datetime

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Event":
        return cls(
            id=d["id"],
            source=d["source"],
            event_type=d["event_type"],
            data=d.get("data", {}),
            status=d["status"],
            created_at=_parse_dt(d["created_at"]) or datetime.now(),
        )


@dataclass(frozen=True)
class Endpoint:
    """A downstream webhook destination."""

    id: str
    url: str
    enabled: bool
    event_types: list[str]
    created_at: datetime

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Endpoint":
        return cls(
            id=d["id"],
            url=d["url"],
            enabled=d.get("enabled", True),
            event_types=d.get("event_types", []),
            created_at=_parse_dt(d["created_at"]) or datetime.now(),
        )


@dataclass(frozen=True)
class Delivery:
    """A delivery attempt of an event to an endpoint."""

    id: str
    event_id: str
    endpoint_id: str
    status: str
    response_status: int | None
    attempt_count: int
    next_retry_at: datetime | None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Delivery":
        return cls(
            id=d["id"],
            event_id=d["event_id"],
            endpoint_id=d["endpoint_id"],
            status=d["status"],
            response_status=d.get("response_status"),
            attempt_count=d.get("attempt_count", 0),
            next_retry_at=_parse_dt(d.get("next_retry_at")),
        )
