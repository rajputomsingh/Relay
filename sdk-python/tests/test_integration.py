"""Server-backed integration tests.

Skipped unless RELAY_TEST_BASE_URL and RELAY_TEST_API_KEY are set.
"""

from __future__ import annotations

import os
import time
from collections.abc import Iterator

import pytest

from relay_python import (
    Relay,
    ValidationError,
)

BASE_URL = os.environ.get("RELAY_TEST_BASE_URL")
API_KEY = os.environ.get("RELAY_TEST_API_KEY")

pytestmark = pytest.mark.skipif(
    not BASE_URL or not API_KEY,
    reason="RELAY_TEST_BASE_URL and RELAY_TEST_API_KEY must be set",
)


@pytest.fixture
def client() -> Iterator[Relay]:
    c = Relay(api_key=API_KEY or "", base_url=BASE_URL or "")
    yield c
    c.close()


def test_end_to_end_lifecycle(client: Relay) -> None:
    # Clean up any leftover endpoints.
    for ep in client.list_endpoints().endpoints:
        if ep.name.startswith("sdk-py-integration-"):
            client.disable_endpoint(ep.id)

    # Create endpoint.
    ep = client.create_endpoint(
        name=f"sdk-py-integration-{int(time.time() * 1000)}",
        url="https://example.com/hook",
        max_attempts=3,
    )
    assert ep.id
    assert ep.enabled is True

    # Send event.
    send = client.send_event(
        source="sdk-py-integration",
        event_type="integration.test",
        data={"k": "v"},
        idempotency_key=f"idem-{int(time.time() * 1000)}",
    )
    assert send.status == "accepted"
    event_id = send.event_id
    assert event_id is not None

    # Inspect.
    evt = client.inspect_event(event_id)
    assert evt.id == event_id
    assert evt.delivery_status  # non-empty

    # List events.
    listing = client.list_events(limit=10)
    assert any(e.id == event_id for e in listing.events)

    # Disable endpoint.
    disabled = client.disable_endpoint(ep.id)
    assert disabled.id == ep.id


def test_replay_409_on_non_failed_event(client: Relay) -> None:
    send = client.send_event(
        source="sdk-py-integration",
        event_type="integration.test",
        data={},
        idempotency_key=f"idem-replay-{int(time.time() * 1000)}",
    )
    event_id = send.event_id
    assert event_id is not None

    # Guard against a fast worker having already failed the event.
    evt = client.inspect_event(event_id)
    if evt.delivery_status == "failed":
        return  # race: worker already terminalized

    with pytest.raises(ValidationError) as exc_info:
        client.replay_event(event_id)
    assert exc_info.value.status == 409
