"""Main Relay SDK client."""

from __future__ import annotations

from typing import Any

from . import exceptions
from ._http import HttpTransport
from .models import (
    AttemptList,
    Delivery,
    Endpoint,
    EndpointList,
    Event,
    EventList,
    EventResponse,
    HealthResponse,
    ReplayResponse,
)


class Relay:
    """Client for the Relay webhook delivery API.

    Example:
        >>> client = Relay(api_key="...", base_url="http://localhost:8080")
        >>> resp = client.send_event(
        ...     source="stripe",
        ...     event_type="payment.completed",
        ...     data={"amount": 5000},
        ...     idempotency_key="order-123",
        ... )
        >>> print(resp.status, resp.event_id)
    """

    def __init__(
        self,
        api_key: str,
        *,
        base_url: str = "http://localhost:8080",
        timeout: float = 30.0,
    ) -> None:
        if not api_key:
            raise exceptions.AuthenticationError("api_key must be provided", status=401)
        self._http = HttpTransport(base_url, api_key, timeout)

    # ============ Events ============

    def send_event(
        self,
        *,
        source: str,
        event_type: str,
        data: dict[str, Any],
        idempotency_key: str,
    ) -> EventResponse:
        """Submit an event for delivery.

        Requires an idempotency key. The returned EventResponse has
        status="accepted" with event_id set on first submission, or
        status="already_accepted" without event_id on retry.
        """
        if not idempotency_key:
            raise ValueError("idempotency_key is required")
        body = self._http.request(
            "POST",
            "/v1/events",
            json={"source": source, "event_type": event_type, "data": data},
            headers={"Idempotency-Key": idempotency_key},
        )
        return EventResponse.from_dict(body)

    def list_events(
        self,
        *,
        delivery_status: str | None = None,
        limit: int | None = None,
        cursor: str | None = None,
    ) -> EventList:
        """List events with optional filters."""
        params: dict[str, str] = {}
        if delivery_status:
            params["delivery_status"] = delivery_status
        if limit is not None:
            params["limit"] = str(limit)
        if cursor:
            params["cursor"] = cursor
        body = self._http.request("GET", "/v1/events", params=params or None)
        return EventList.from_dict(body)

    def inspect_event(self, event_id: str) -> Event:
        """Get the current state of an event by ID."""
        if not event_id:
            raise ValueError("event_id is required")
        body = self._http.request("GET", f"/v1/events/{event_id}")
        return Event.from_dict(body)

    def replay_event(self, event_id: str) -> ReplayResponse:
        """Requeue failed deliveries for a failed event.

        Raises ValidationError (HTTP 409) if the event is not in `failed`
        state or has no failed deliveries.
        """
        if not event_id:
            raise ValueError("event_id is required")
        body = self._http.request("POST", f"/v1/events/{event_id}/replay")
        return ReplayResponse.from_dict(body)

    def get_deliveries(self, event_id: str) -> list[Delivery]:
        """Get all delivery records for an event."""
        if not event_id:
            raise ValueError("event_id is required")
        body = self._http.request("GET", f"/v1/events/{event_id}/deliveries")
        return [Delivery.from_dict(d) for d in body.get("deliveries", [])]

    def get_attempts(
        self,
        event_id: str,
        *,
        limit: int | None = None,
        cursor: str | None = None,
    ) -> AttemptList:
        """Get the attempt history for an event."""
        if not event_id:
            raise ValueError("event_id is required")
        params: dict[str, str] = {}
        if limit is not None:
            params["limit"] = str(limit)
        if cursor:
            params["cursor"] = cursor
        body = self._http.request(
            "GET",
            f"/v1/events/{event_id}/attempts",
            params=params or None,
        )
        return AttemptList.from_dict(body)

    # ============ Endpoints ============

    def create_endpoint(
        self,
        *,
        name: str,
        url: str,
        signing_secret: str | None = None,
        event_filter: Any = None,
        max_attempts: int = 3,
        rate_limit_rps: int = 0,
        enabled: bool = True,
    ) -> Endpoint:
        """Register a new webhook endpoint."""
        if not name:
            raise ValueError("name is required")
        if not url:
            raise ValueError("url is required")
        payload: dict[str, Any] = {
            "name": name,
            "url": url,
            "max_attempts": max_attempts,
            "rate_limit_rps": rate_limit_rps,
            "enabled": enabled,
        }
        if signing_secret is not None:
            payload["signing_secret"] = signing_secret
        if event_filter is not None:
            # Accept either EventFilter or plain dict
            if hasattr(event_filter, "to_dict"):
                payload["event_filter"] = event_filter.to_dict()
            else:
                payload["event_filter"] = event_filter
        body = self._http.request("POST", "/v1/endpoints", json=payload)
        return Endpoint.from_dict(body)

    def list_endpoints(self) -> EndpointList:
        """List all registered webhook endpoints."""
        body = self._http.request("GET", "/v1/endpoints")
        return EndpointList.from_dict(body)

    def get_endpoint(self, endpoint_id: str) -> Endpoint:
        """Get a single endpoint by ID."""
        if not endpoint_id:
            raise ValueError("endpoint_id is required")
        body = self._http.request("GET", f"/v1/endpoints/{endpoint_id}")
        return Endpoint.from_dict(body)

    def update_endpoint(
        self,
        endpoint_id: str,
        *,
        name: str | None = None,
        url: str | None = None,
        signing_secret: str | None = None,
        event_filter: Any = None,
        max_attempts: int | None = None,
        rate_limit_rps: int | None = None,
        enabled: bool | None = None,
    ) -> Endpoint:
        """Apply a partial update to an endpoint."""
        if not endpoint_id:
            raise ValueError("endpoint_id is required")
        payload: dict[str, Any] = {}
        if name is not None:
            payload["name"] = name
        if url is not None:
            payload["url"] = url
        if signing_secret is not None:
            payload["signing_secret"] = signing_secret
        if event_filter is not None:
            if hasattr(event_filter, "to_dict"):
                payload["event_filter"] = event_filter.to_dict()
            else:
                payload["event_filter"] = event_filter
        if max_attempts is not None:
            payload["max_attempts"] = max_attempts
        if rate_limit_rps is not None:
            payload["rate_limit_rps"] = rate_limit_rps
        if enabled is not None:
            payload["enabled"] = enabled
        body = self._http.request("PATCH", f"/v1/endpoints/{endpoint_id}", json=payload)
        return Endpoint.from_dict(body)

    def disable_endpoint(self, endpoint_id: str) -> Endpoint:
        """Soft-delete (disable) an endpoint.

        The server returns {"status": "disabled", "id": "..."}, not the
        full Endpoint shape. Only `id` will be populated on the return.
        Call get_endpoint after disable if you need the updated fields.
        """
        if not endpoint_id:
            raise ValueError("endpoint_id is required")
        body = self._http.request("DELETE", f"/v1/endpoints/{endpoint_id}")
        # Server returns {"status": "disabled", "id": "..."} — synthesize
        # a minimal Endpoint with just the ID populated.
        return Endpoint(
            id=body.get("id", endpoint_id),
            name="",
            url="",
            has_signing_secret=False,
            enabled=False,
            max_attempts=0,
            event_filter=None,  # type: ignore[arg-type]
        )

    # ============ Health ============

    def health(self) -> HealthResponse:
        """Liveness probe."""
        body = self._http.request("GET", "/healthz")
        return HealthResponse.from_dict(body)

    def ready(self) -> HealthResponse:
        """Readiness probe."""
        body = self._http.request("GET", "/readyz")
        return HealthResponse.from_dict(body)

    # ============ Lifecycle ============

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self._http.close()

    def __enter__(self) -> Relay:
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()
