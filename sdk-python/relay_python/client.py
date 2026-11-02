"""Main Relay SDK client."""
from typing import Any

from . import exceptions
from ._http import HttpTransport
from .models import Delivery, Endpoint


class Relay:
    """Client for the Relay webhook delivery API.

    Example:
        >>> client = Relay(api_key="...", base_url="http://localhost:8080")
        >>> event = client.send_event(
        ...     source="stripe",
        ...     event_type="payment.completed",
        ...     data={"amount": 5000},
        ...     idempotency_key="order-123",
        ... )
    """

    def __init__(
        self,
        api_key: str,
        *,
        base_url: str = "http://localhost:8080",
        timeout: float = 30.0,
    ) -> None:
        if not api_key:
            raise exceptions.AuthenticationError(
                "api_key must be provided", status=401
            )
        self._http = HttpTransport(base_url, api_key, timeout)

    # ============ Events ============

    def send_event(
        self,
        *,
        source: str,
        event_type: str,
        data: dict[str, Any],
        idempotency_key: str | None = None,
    ) -> dict[str, Any]:
        """Submit an event for delivery."""
        headers: dict[str, str] = {}
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        return self._http.request(
            "POST",
            "/v1/events",
            json={"source": source, "event_type": event_type, "data": data},
            headers=headers,
        )

    def replay_event(self, event_id: str) -> dict[str, Any]:
        """Replay a previously failed event."""
        return self._http.request("POST", f"/v1/events/{event_id}/replay")

    def get_deliveries(self, event_id: str) -> list[Delivery]:
        """Get all delivery attempts for an event."""
        body = self._http.request("GET", f"/v1/events/{event_id}/deliveries")
        items = body.get("deliveries") or body.get("items") or []
        return [Delivery.from_dict(d) for d in items]

    def get_attempts(self, event_id: str) -> list[dict[str, Any]]:
        """Get detailed attempt history for an event."""
        body = self._http.request("GET", f"/v1/events/{event_id}/attempts")
        attempts: list[dict[str, Any]] = body.get("attempts", [])
        return attempts

    # ============ Endpoints ============

    def list_endpoints(self) -> list[Endpoint]:
        """List all registered webhook endpoints."""
        body = self._http.request("GET", "/v1/endpoints")
        items = body.get("endpoints") or body.get("items") or []
        return [Endpoint.from_dict(e) for e in items]

    def get_endpoint(self, endpoint_id: str) -> Endpoint:
        """Get a single endpoint by ID."""
        body = self._http.request("GET", f"/v1/endpoints/{endpoint_id}")
        return Endpoint.from_dict(body)

    def create_endpoint(
        self,
        *,
        url: str,
        event_types: list[str] | None = None,
        secret: str | None = None,
    ) -> Endpoint:
        """Register a new webhook endpoint."""
        payload: dict[str, Any] = {"url": url}
        if event_types:
            payload["event_types"] = event_types
        if secret:
            payload["secret"] = secret
        body = self._http.request("POST", "/v1/endpoints", json=payload)
        return Endpoint.from_dict(body)

    def update_endpoint(
        self,
        endpoint_id: str,
        *,
        url: str | None = None,
        enabled: bool | None = None,
        event_types: list[str] | None = None,
    ) -> Endpoint:
        """Update an existing endpoint."""
        payload: dict[str, Any] = {}
        if url is not None:
            payload["url"] = url
        if enabled is not None:
            payload["enabled"] = enabled
        if event_types is not None:
            payload["event_types"] = event_types
        body = self._http.request(
            "PATCH", f"/v1/endpoints/{endpoint_id}", json=payload
        )
        return Endpoint.from_dict(body)

    def disable_endpoint(self, endpoint_id: str) -> None:
        """Soft-delete (disable) an endpoint."""
        self._http.request("DELETE", f"/v1/endpoints/{endpoint_id}")

    # ============ Lifecycle ============

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self._http.close()

    def __enter__(self) -> "Relay":
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()
