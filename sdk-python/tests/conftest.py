"""Pytest fixtures for the Relay SDK test suite."""

import pytest

from relay_python import Relay


@pytest.fixture
def client() -> Relay:
    return Relay(api_key="test_key", base_url="http://test")
