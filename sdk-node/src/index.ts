import { relayErrorForStatus } from "./errors.js";
import type {
  CreateEndpointParams,
  Endpoint,
  EndpointHealthResponse,
  EventInspection,
  EventPayload,
  EventResponse,
  HealthResponse,
  ListAttemptsParams,
  ListAttemptsResponse,
  ListDeliveriesResponse,
  ListEndpointsResponse,
  ListEventsParams,
  ListEventsResponse,
  ReplayResponse,
  UpdateEndpointParams,
} from "./types.js";

export * from "./errors.js";
export * from "./signatures.js";
export * from "./types.js";

export interface RelayConfig {
  apiKey: string;
  baseUrl?: string;
  /** Optional custom fetch (for tests or non-Node runtimes). */
  fetch?: typeof fetch;
}

export class Relay {
  private readonly apiKey: string;
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(config: RelayConfig) {
    if (!config.apiKey) {
      throw new Error("apiKey is required");
    }
    this.apiKey = config.apiKey;
    this.baseUrl = (config.baseUrl || "http://localhost:8080").replace(
      /\/$/,
      "",
    );
    this.fetchImpl = config.fetch ?? globalThis.fetch;
  }

  // -------------------------------------------------------------------
  // Internal HTTP helper
  // -------------------------------------------------------------------

  private async request<T>(
    method: string,
    path: string,
    options: { body?: unknown; headers?: Record<string, string> } = {},
  ): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.apiKey}`,
      "User-Agent": "relay-node/2.0.0",
      ...options.headers,
    };

    let body: string | undefined;
    if (options.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(options.body);
    }

    const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method,
      headers,
      body,
    });

    if (!res.ok) {
      let parsed: unknown = null;
      try {
        parsed = await res.json();
      } catch {
        // ignore parse errors
      }
      const message =
        (parsed as { error?: string } | null)?.error ??
        res.statusText ??
        "unknown error";
      throw relayErrorForStatus(res.status, message, parsed);
    }

    // DELETE endpoints return a small JSON body; all others return JSON too.
    if (res.status === 204) {
      return undefined as T;
    }
    return (await res.json()) as T;
  }

  // -------------------------------------------------------------------
  // Events
  // -------------------------------------------------------------------

  /**
   * Submit an event. Requires an idempotency key.
   *
   * Response is `accepted` with an event_id on first submission, or
   * `already_accepted` without an event_id on retry. Check `status`
   * before using `event_id`.
   */
  async sendEvent(
    payload: EventPayload,
    idempotencyKey: string,
  ): Promise<EventResponse> {
    if (!idempotencyKey) {
      throw new Error("idempotencyKey is required");
    }
    return this.request<EventResponse>("POST", "/v1/events", {
      body: payload,
      headers: { "Idempotency-Key": idempotencyKey },
    });
  }

  async listEvents(params: ListEventsParams = {}): Promise<ListEventsResponse> {
    const q = new URLSearchParams();
    if (params.delivery_status)
      q.set("delivery_status", params.delivery_status);
    if (params.limit) q.set("limit", String(params.limit));
    if (params.cursor) q.set("cursor", params.cursor);
    const suffix = q.toString() ? `?${q.toString()}` : "";
    return this.request<ListEventsResponse>("GET", `/v1/events${suffix}`);
  }

  async inspectEvent(eventId: string): Promise<EventInspection> {
    if (!eventId) throw new Error("eventId is required");
    return this.request<EventInspection>(
      "GET",
      `/v1/events/${encodeURIComponent(eventId)}`,
    );
  }

  async replayEvent(eventId: string): Promise<ReplayResponse> {
    if (!eventId) throw new Error("eventId is required");
    return this.request<ReplayResponse>(
      "POST",
      `/v1/events/${encodeURIComponent(eventId)}/replay`,
    );
  }

  async listDeliveries(eventId: string): Promise<ListDeliveriesResponse> {
    if (!eventId) throw new Error("eventId is required");
    return this.request<ListDeliveriesResponse>(
      "GET",
      `/v1/events/${encodeURIComponent(eventId)}/deliveries`,
    );
  }

  async listAttempts(
    eventId: string,
    params: ListAttemptsParams = {},
  ): Promise<ListAttemptsResponse> {
    if (!eventId) throw new Error("eventId is required");
    const q = new URLSearchParams();
    if (params.limit) q.set("limit", String(params.limit));
    if (params.cursor) q.set("cursor", params.cursor);
    const suffix = q.toString() ? `?${q.toString()}` : "";
    return this.request<ListAttemptsResponse>(
      "GET",
      `/v1/events/${encodeURIComponent(eventId)}/attempts${suffix}`,
    );
  }

  // -------------------------------------------------------------------
  // Endpoints
  // -------------------------------------------------------------------

  async createEndpoint(params: CreateEndpointParams): Promise<Endpoint> {
    return this.request<Endpoint>("POST", "/v1/endpoints", { body: params });
  }

  async listEndpoints(): Promise<ListEndpointsResponse> {
    return this.request<ListEndpointsResponse>("GET", "/v1/endpoints");
  }

  async getEndpoint(endpointId: string): Promise<Endpoint> {
    if (!endpointId) throw new Error("endpointId is required");
    return this.request<Endpoint>(
      "GET",
      `/v1/endpoints/${encodeURIComponent(endpointId)}`,
    );
  }

  /**
   * Fetch delivery health for one endpoint.
   *
   * Returns one-hour attempt counts and success rate, consecutive
   * delivery failures, and the latest success/failure timestamps.
   * Timestamps are null when no corresponding outcome exists.
   */
  async getEndpointHealth(
    endpointId: string,
  ): Promise<EndpointHealthResponse> {
    if (!endpointId) throw new Error("endpointId is required");
    return this.request<EndpointHealthResponse>(
      "GET",
      `/v1/endpoints/${encodeURIComponent(endpointId)}/health`,
    );
  }

  async updateEndpoint(
    endpointId: string,
    params: UpdateEndpointParams,
  ): Promise<Endpoint> {
    if (!endpointId) throw new Error("endpointId is required");
    return this.request<Endpoint>(
      "PATCH",
      `/v1/endpoints/${encodeURIComponent(endpointId)}`,
      { body: params },
    );
  }

  /**
   * Soft-delete an endpoint. The endpoint is not removed from the
   * database; it is marked disabled and stops receiving deliveries.
   *
   * The server returns `{"status": "disabled", "id": "..."}`, which does
   * not include the full Endpoint shape. Only `id` will be populated.
   * Call `getEndpoint` after disable if you need the updated fields.
   */
  async disableEndpoint(endpointId: string): Promise<Endpoint> {
    if (!endpointId) throw new Error("endpointId is required");
    return this.request<Endpoint>(
      "DELETE",
      `/v1/endpoints/${encodeURIComponent(endpointId)}`,
    );
  }

  // -------------------------------------------------------------------
  // Health
  // -------------------------------------------------------------------

  async health(): Promise<HealthResponse> {
    return this.request<HealthResponse>("GET", "/healthz");
  }

  async ready(): Promise<HealthResponse> {
    return this.request<HealthResponse>("GET", "/readyz");
  }
}
