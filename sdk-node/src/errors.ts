/**
 * Error thrown when the Relay API returns a non-2xx response.
 * The HTTP status code and parsed error message are exposed as fields.
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