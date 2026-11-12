import { afterEach, describe, expect, it, vi } from "vitest";
import { Relay, RelayAPIError, signWebhookBody } from "./index.js";
import { verifyWebhookSignature, SignatureError } from "./signatures.js";

const apiKey = "test-api-key-with-at-least-32-chars";
const baseUrl = "http://test.local";

function mockResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("Relay", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sendEvent sends idempotency key and decodes response", async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      mockResponse(202, {
        status: "accepted",
        event_id: "6af2f46f64e24ff56d2b3bab",
        idempotency_key: "idem-123",
      }),
    );
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    const resp = await client.sendEvent(
      { source: "test", event_type: "test.created", data: {} },
      "idem-123",
    );

    expect(resp.status).toBe("accepted");
    expect(resp.event_id).toBe("6af2f46f64e24ff56d2b3bab");
    expect(resp.idempotency_key).toBe("idem-123");

    const call = fetchMock.mock.calls[0];
    expect(call[0]).toBe("http://test.local/v1/events");
    const init = call[1] as RequestInit;
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe(
      "idem-123",
    );
  });

  it("sendEvent throws when idempotencyKey is empty", async () => {
    const fetchMock = vi.fn();
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    await expect(
      client.sendEvent(
        { source: "test", event_type: "test.created", data: {} },
        "",
      ),
    ).rejects.toThrow("idempotencyKey is required");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("sendEvent returns already_accepted without event_id on retry", async () => {
    const fetchMock = vi.fn(async () =>
      mockResponse(200, {
        status: "already_accepted",
        idempotency_key: "idem-123",
      }),
    );
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    const resp = await client.sendEvent(
      { source: "test", event_type: "test.created", data: {} },
      "idem-123",
    );

    expect(resp.status).toBe("already_accepted");
    expect(resp.event_id).toBeUndefined();
  });

  it("inspectEvent decodes parent event state", async () => {
    const fetchMock = vi.fn(async () =>
      mockResponse(200, {
        id: "evt_1",
        source: "test",
        event_type: "test.created",
        data: {},
        idempotency_key: "idem-1",
        received_at: "2026-11-10T17:00:00Z",
        status: "accepted",
        delivery_status: "delivered",
        delivery_attempts: 1,
      }),
    );
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    const evt = await client.inspectEvent("evt_1");
    expect(evt.delivery_status).toBe("delivered");
    expect(evt.delivery_attempts).toBe(1);
  });

  it("listEvents serializes filter query parameters", async () => {
    const fetchMock = vi.fn(async (url: string) => {
      expect(url).toBe(
        "http://test.local/v1/events?delivery_status=failed&limit=20",
      );
      return mockResponse(200, {
        events: [],
        limit: 20,
        has_more: false,
      });
    });
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    await client.listEvents({ delivery_status: "failed", limit: 20 });
  });

  it("replayEvent surfaces 409 as RelayAPIError with isConflict()", async () => {
    const fetchMock = vi.fn(async () =>
      mockResponse(409, { error: "event not in failed state" }),
    );
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    await expect(client.replayEvent("evt_1")).rejects.toMatchObject({
      status: 409,
      message: "event not in failed state",
    });

    try {
      await client.replayEvent("evt_1");
    } catch (e) {
      expect(e).toBeInstanceOf(RelayAPIError);
      expect((e as RelayAPIError).isConflict()).toBe(true);
    }
  });

  it("createEndpoint sends correctly shaped body", async () => {
    let captured: unknown = null;
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      captured = JSON.parse(init?.body as string);
      return mockResponse(201, {
        id: "ep_1",
        name: "test",
        url: "https://example.com/hook",
        has_signing_secret: false,
        enabled: true,
        max_attempts: 3,
        created_at: "2026-11-10T17:00:00Z",
        updated_at: "2026-11-10T17:00:00Z",
      });
    });
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    await client.createEndpoint({
      name: "test",
      url: "https://example.com/hook",
      max_attempts: 3,
    });

    expect(captured).toEqual({
      name: "test",
      url: "https://example.com/hook",
      max_attempts: 3,
    });
  });

  it("listEndpoints decodes count and endpoints array", async () => {
    const fetchMock = vi.fn(async () =>
      mockResponse(200, { endpoints: [], count: 0 }),
    );
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    const resp = await client.listEndpoints();
    expect(resp.count).toBe(0);
    expect(resp.endpoints).toEqual([]);
  });

  it("getEndpointHealth requests the endpoint health path and decodes response", async () => {
    let requestedUrl: string | undefined;
    const fetchMock = vi.fn(async (url: string) => {
      requestedUrl = url;
      return mockResponse(200, {
        success_rate_1h: 75,
        attempts_1h: 4,
        successes_1h: 3,
        failures_1h: 1,
        consecutive_failures: 2,
        last_success_at: "2026-11-11T10:00:00Z",
        last_failure_at: null,
      });
    });
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    const resp = await client.getEndpointHealth("ep_1");

    expect(requestedUrl).toBe("http://test.local/v1/endpoints/ep_1/health");
    expect(resp.success_rate_1h).toBe(75);
    expect(resp.attempts_1h).toBe(4);
    expect(resp.consecutive_failures).toBe(2);
    expect(resp.last_failure_at).toBeNull();
  });

  it("getEndpointHealth requires an endpoint id", async () => {
    const fetchMock = vi.fn();
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    await expect(client.getEndpointHealth("")).rejects.toThrow(
      /endpointId is required/,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("updateEndpoint sends PATCH with partial body", async () => {
    let method: string | undefined;
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      method = init?.method;
      return mockResponse(200, {
        id: "ep_1",
        name: "updated",
        url: "https://example.com/hook",
        has_signing_secret: false,
        enabled: false,
        max_attempts: 3,
        created_at: "2026-11-10T17:00:00Z",
        updated_at: "2026-11-10T17:00:00Z",
      });
    });
    const client = new Relay({
      apiKey,
      baseUrl,
      fetch: fetchMock as typeof fetch,
    });

    const ep = await client.updateEndpoint("ep_1", { enabled: false });
    expect(method).toBe("PATCH");
    expect(ep.enabled).toBe(false);
  });

  it("verifyWebhookSignature accepts a valid fresh signature", () => {
    const secret = "0123456789abcdef0123456789abcdef";
    const body = '{"event_id":"abc"}';
    const ts = Math.floor(Date.now() / 1000);
    const sig = signWebhookBody(secret, ts, body);

    expect(() =>
      verifyWebhookSignature(secret, String(ts), sig, body),
    ).not.toThrow();
  });

  it("verifyWebhookSignature rejects tampered body", () => {
    const secret = "0123456789abcdef0123456789abcdef";
    const ts = Math.floor(Date.now() / 1000);
    const sig = signWebhookBody(secret, ts, '{"event_id":"abc"}');

    expect(() =>
      verifyWebhookSignature(secret, String(ts), sig, '{"event_id":"xyz"}'),
    ).toThrow(SignatureError);
  });

  it("verifyWebhookSignature rejects stale timestamp", () => {
    const secret = "0123456789abcdef0123456789abcdef";
    const body = '{"event_id":"abc"}';
    const staleTs = Math.floor(Date.now() / 1000) - 600;
    const sig = signWebhookBody(secret, staleTs, body);

    expect(() =>
      verifyWebhookSignature(secret, String(staleTs), sig, body),
    ).toThrow(/freshness window/);
  });

  it("verifyWebhookSignature rejects future timestamp", () => {
    const secret = "0123456789abcdef0123456789abcdef";
    const body = '{"event_id":"abc"}';
    const futureTs = Math.floor(Date.now() / 1000) + 600;
    const sig = signWebhookBody(secret, futureTs, body);

    expect(() =>
      verifyWebhookSignature(secret, String(futureTs), sig, body),
    ).toThrow(/freshness window/);
  });
});
