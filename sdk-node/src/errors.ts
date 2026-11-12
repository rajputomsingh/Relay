/**
 * Error thrown when the Relay API returns a non-2xx response.
 * The HTTP status code and parsed error message are exposed as fields.
 *
 * Typed subclasses (BadRequestError, UnauthorizedError, NotFoundError,
 * RateLimitedError, ServerError) extend this class, so `instanceof
 * RelayAPIError` catches every API failure.
 */
export class RelayAPIError extends Error {
  public readonly status: number;
  public readonly body?: unknown;

  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.name = "RelayAPIError";
    this.status = status;
    this.body = body;
  }

  isNotFound(): boolean {
    return this.status === 404;
  }

  isConflict(): boolean {
    return this.status === 409;
  }

  isUnauthorized(): boolean {
    return this.status === 401;
  }

  isRateLimited(): boolean {
    return this.status === 429;
  }

  isServerError(): boolean {
    return this.status >= 500;
  }
}

/**
 * Thrown for HTTP 400 responses.
 */
export class BadRequestError extends RelayAPIError {
  constructor(status: number, message: string, body?: unknown) {
    super(status, message, body);
    this.name = "BadRequestError";
  }
}

/**
 * Thrown for HTTP 401 responses.
 */
export class UnauthorizedError extends RelayAPIError {
  constructor(status: number, message: string, body?: unknown) {
    super(status, message, body);
    this.name = "UnauthorizedError";
  }
}

/**
 * Thrown for HTTP 404 responses.
 */
export class NotFoundError extends RelayAPIError {
  constructor(status: number, message: string, body?: unknown) {
    super(status, message, body);
    this.name = "NotFoundError";
  }
}

/**
 * Thrown for HTTP 429 responses.
 */
export class RateLimitedError extends RelayAPIError {
  constructor(status: number, message: string, body?: unknown) {
    super(status, message, body);
    this.name = "RateLimitedError";
  }
}

/**
 * Thrown for HTTP 5xx responses.
 */
export class ServerError extends RelayAPIError {
  constructor(status: number, message: string, body?: unknown) {
    super(status, message, body);
    this.name = "ServerError";
  }
}

/**
 * Build the most specific RelayAPIError for an HTTP status.
 *
 * Statuses without a dedicated subclass (e.g. 403, 409) fall back to the
 * base {@link RelayAPIError}, preserving the existing behavior.
 */
export function relayErrorForStatus(
  status: number,
  message: string,
  body?: unknown,
): RelayAPIError {
  switch (true) {
    case status === 400:
      return new BadRequestError(status, message, body);
    case status === 401:
      return new UnauthorizedError(status, message, body);
    case status === 404:
      return new NotFoundError(status, message, body);
    case status === 429:
      return new RateLimitedError(status, message, body);
    case status >= 500:
      return new ServerError(status, message, body);
    default:
      return new RelayAPIError(status, message, body);
  }
}