"""Unit tests for the Relay SDK client using mocked HTTP."""

from __future__ import annotations

import json

import pytest
from pytest_httpx import HTTPXMock

from relay_python import (
    AuthenticationError,
    NotFoundError,
    Relay,
    ValidationError,
)

BASE = "http://test"


def _client() -> Relay:
    return Relay(api_key="test_key", base_url=BASE)


def test_send_event_returns_typed_response(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/v1/events",
        status_code=202,
        json={
            "status": "accepted",
            "event_id": "6af2f46f64e24ff56d2b3bab",
            "idempotency_key": "idem-123",
        },
    )

    with _client() as client:
        resp = client.send_event(
            source="stripe",
            event_type="payment.completed",
            data={"amount": 5000},
            idempotency_key="idem-123",
        )

    assert resp.status == "accepted"
    assert resp.event_id == "6af2f46f64e24ff56d2b3bab"
    assert resp.idempotency_key == "idem-123"

    req = httpx_mock.get_requests()[0]
    assert req.headers["idempotency-key"] == "idem-123"
    assert req.headers["authorization"] == "Bearer test_key"


def test_send_event_already_accepted_has_no_event_id(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/v1/events",
        status_code=200,
        json={"status": "already_accepted", "idempotency_key": "idem-1"},
    )

    with _client() as client:
        resp = client.send_event(
            source="test",
            event_type="test.created",
            data={},
            idempotency_key="idem-1",
        )

    assert resp.status == "already_accepted"
    assert resp.event_id is None


def test_send_event_requires_idempotency_key() -> None:
    with _client() as client, pytest.raises(ValueError, match="idempotency_key is required"):
        client.send_event(
            source="test",
            event_type="test.created",
            data={},
            idempotency_key="",
        )


def test_inspect_event_decodes_all_fields(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/v1/events/evt_1",
        json={
            "id": "evt_1",
            "source": "test",
            "event_type": "test.created",
            "data": {"k": "v"},
            "idempotency_key": "idem-1",
            "received_at": "2026-11-10T17:00:00Z",
            "status": "accepted",
            "delivery_status": "delivered",
            "delivery_attempts": 1,
            "delivered_at": "2026-11-10T17:00:05Z",
        },
    )

    with _client() as client:
        evt = client.inspect_event("evt_1")

    assert evt.delivery_status == "delivered"
    assert evt.delivery_attempts == 1
    assert evt.delivered_at is not None


def test_replay_surfaces_409_as_validation_error(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/v1/events/evt_1/replay",
        status_code=409,
        json={"error": "event not in failed state"},
    )

    with _client() as client, pytest.raises(ValidationError) as exc_info:
        client.replay_event("evt_1")

    assert exc_info.value.status == 409


def test_authentication_error(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/v1/endpoints",
        status_code=401,
        json={"error": "unauthorized"},
    )

    with _client() as client, pytest.raises(AuthenticationError) as exc_info:
        client.list_endpoints()
    assert exc_info.value.status == 401


def test_get_endpoint_not_found(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/v1/endpoints/missing",
        status_code=404,
        json={"error": "not found"},
    )

    with _client() as client, pytest.raises(NotFoundError):
        client.get_endpoint("missing")


def test_list_events_serializes_params(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/v1/events?delivery_status=failed&limit=20",
        json={"events": [], "limit": 20, "has_more": False},
    )

    with _client() as client:
        client.list_events(delivery_status="failed", limit=20)


def test_create_endpoint_requires_name() -> None:
    with _client() as client, pytest.raises(ValueError, match="name is required"):
        client.create_endpoint(name="", url="https://example.com/hook")


def test_create_endpoint_serializes_correct_body(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/v1/endpoints",
        status_code=201,
        json={
            "id": "ep_1",
            "name": "test",
            "url": "https://example.com/hook",
            "has_signing_secret": False,
            "enabled": True,
            "max_attempts": 3,
            "event_filter": {},
        },
    )

    with _client() as client:
        client.create_endpoint(
            name="test",
            url="https://example.com/hook",
            max_attempts=3,
        )

    req = httpx_mock.get_requests()[0]
    body = json.loads(req.content)
    assert body["name"] == "test"
    assert body["url"] == "https://example.com/hook"
    assert body["max_attempts"] == 3


def test_disable_endpoint_returns_minimal_endpoint(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="DELETE",
        url=f"{BASE}/v1/endpoints/ep_1",
        json={"status": "disabled", "id": "ep_1"},
    )

    with _client() as client:
        ep = client.disable_endpoint("ep_1")

    assert ep.id == "ep_1"
    assert ep.enabled is False


def test_health_and_ready(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/healthz",
        json={"service": "relay", "status": "ok"},
    )
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/readyz",
        json={"status": "ready", "database": "connected"},
    )

    with _client() as client:
        h = client.health()
        r = client.ready()

    assert h.status == "ok"
    assert r.status == "ready"
