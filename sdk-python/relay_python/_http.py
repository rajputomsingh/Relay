"""Internal HTTP transport."""
from typing import Any

import httpx

from .exceptions import _raise_for_status


class HttpTransport:
    """Thin wrapper over httpx.Client with error mapping."""

    def __init__(self, base_url: str, api_key: str, timeout: float) -> None:
        self._client = httpx.Client(
            base_url=base_url.rstrip("/"),
            headers={
                "Authorization": f"Bearer {api_key}",
                "User-Agent": "relay-python/0.1.0",
                "Content-Type": "application/json",
            },
            timeout=timeout,
        )

    def close(self) -> None:
        self._client.close()

    def request(
        self,
        method: str,
        path: str,
        *,
        json: Any = None,
        headers: dict[str, str] | None = None,
    ) -> dict[str, Any]:
        resp = self._client.request(method, path, json=json, headers=headers)
        body: dict[str, Any] = {}
        if resp.content:
            try:
                body = resp.json()
            except ValueError:
                body = {}
        code = body.get("code") if isinstance(body, dict) else None
        message = body.get("error") or resp.text or resp.reason_phrase
        _raise_for_status(resp.status_code, code, message)
        return body

    def __enter__(self) -> "HttpTransport":
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()
