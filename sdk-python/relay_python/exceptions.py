"""Exception hierarchy for the Relay SDK."""


class RelayError(Exception):
    """Base exception for all Relay SDK errors."""

    def __init__(
        self,
        message: str,
        *,
        status: int | None = None,
        code: str | None = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.status = status
        self.code = code

    def __repr__(self) -> str:
        parts = [f"message={self.message!r}"]
        if self.status is not None:
            parts.append(f"status={self.status}")
        if self.code:
            parts.append(f"code={self.code!r}")
        return f"{self.__class__.__name__}({', '.join(parts)})"


class AuthenticationError(RelayError):
    """401/403 — Invalid or missing API key."""


class NotFoundError(RelayError):
    """404 — Resource does not exist."""


class ValidationError(RelayError):
    """400/409 — Request body or parameters are invalid."""


class RateLimitError(RelayError):
    """429 — Rate limit exceeded."""


class ServerError(RelayError):
    """5xx — Server-side error."""


def _raise_for_status(status: int, code: str | None, message: str) -> None:
    """Raise the appropriate exception based on HTTP status code."""
    if 200 <= status < 300:
        return

    status_map: dict[int, type[RelayError]] = {
        400: ValidationError,
        401: AuthenticationError,
        403: AuthenticationError,
        404: NotFoundError,
        409: ValidationError,
        429: RateLimitError,
    }

    if status in status_map:
        exc_cls = status_map[status]
    elif status >= 500:
        exc_cls = ServerError
    else:
        exc_cls = RelayError

    raise exc_cls(message, status=status, code=code)
