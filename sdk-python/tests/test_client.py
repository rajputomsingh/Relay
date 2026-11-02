"""Unit tests for the Relay SDK client."""
import pytest
from pytest_httpx import HTTPXMock

from relay_python import AuthenticationError, NotFoundError, Relay


def test_send_event(client: Relay, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url="http://test/v1/events",
        json={"id": "evt_123", "status": "pending"},
    )
    result = client.send_event(
        source="stripe",
        event_type="payment.completed",
        data={"amount": 5000},
        idempotency_key="order-123",
    )
    assert result["id"] == "evt_123"
    request = httpx_mock.get_requests()[0]
    assert request.headers["idempotency-key"] == "order-123"
    assert request.headers["authorization"] == "Bearer test_key"


def test_send_event_without_idempotency_key(
    client: Relay, httpx_mock: HTTPXMock
) -> None:
    httpx_mock.add_response(
        method="POST",
        url="http://test/v1/events",
        json={"id": "evt_456"},
    )
    client.send_event(source="test", event_type="test.event", data={})
    assert "idempotency-key" not in httpx_mock.get_requests()[0].headers


def test_authentication_error(client: Relay, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url="http://test/v1/endpoints",
        status_code=401,
        json={"error": "Unauthorized"},
    )
    with pytest.raises(AuthenticationError) as exc_info:
        client.list_endpoints()
    assert exc_info.value.status == 401


def test_not_found_error(client: Relay, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url="http://test/v1/events/missing/replay",
        status_code=404,
        json={"error": "Event not found"},
    )
    with pytest.raises(NotFoundError):
        client.replay_event("missing")


def test_list_endpoints(client: Relay, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url="http://test/v1/endpoints",
        json={
            "endpoints": [
                {
                    "id": "ep_1",
                    "url": "https://customer.com/webhook",
                    "enabled": True,
                    "event_types": ["payment.completed"],
                    "created_at": "2026-11-01T10:00:00Z",
                }
            ]
        },
    )
    endpoints = client.list_endpoints()
    assert len(endpoints) == 1
    assert endpoints[0].url == "https://customer.com/webhook"


def test_create_endpoint(client: Relay, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url="http://test/v1/endpoints",
        json={
            "id": "ep_2",
            "url": "https://new.com/webhook",
            "enabled": True,
            "event_types": ["user.created"],
            "created_at": "2026-11-01T10:00:00Z",
        },
    )
    ep = client.create_endpoint(
        url="https://new.com/webhook", event_types=["user.created"]
    )
    assert ep.id == "ep_2"


def test_context_manager() -> None:
    with Relay(api_key="test") as client:
        assert client is not None
