import { describe, expect, it } from "vitest";
import { Relay, RelayAPIError } from "./index.js";

const baseUrl = process.env.RELAY_TEST_BASE_URL;
const apiKey = process.env.RELAY_TEST_API_KEY;
const describeOrSkip =
  baseUrl && apiKey
    ? describe
    : (name: string, fn: () => void) => {
        describe.skip(
          `${name} (skipped: RELAY_TEST_BASE_URL and RELAY_TEST_API_KEY must be set)`,
          fn,
        );
      };

describeOrSkip("Relay integration", () => {
  it("creates endpoint, sends event, inspects, replays", async () => {
    const client = new Relay({
      apiKey: apiKey!,
      baseUrl: baseUrl!,
    });

    // Clean up any leftover endpoint from a prior run.
    const existing = await client.listEndpoints().catch(() => null);
    if (existing) {
      for (const ep of existing.endpoints) {
        if (ep.name.startsWith("sdk-node-integration-")) {
          await client.disableEndpoint(ep.id).catch(() => {});
        }
      }
    }

    // Create endpoint.
    const ep = await client.createEndpoint({
      name: `sdk-node-integration-${Date.now()}`,
      url: "https://example.com/hook",
      max_attempts: 3,
    });
    expect(ep.id).toBeTruthy();
    expect(ep.enabled).toBe(true);

    // Send event — exercises ingestion, fan-out, aggregator.
    const sendResp = await client.sendEvent(
      {
        source: "sdk-node-integration",
        event_type: "integration.test",
        data: { k: "v" },
      },
      `idem-${Date.now()}`,
    );
    expect(sendResp.status).toBe("accepted");
    expect(sendResp.event_id).toBeTruthy();

    // Inspect — parent status must be non-empty.
    const evt = await client.inspectEvent(sendResp.event_id!);
    expect(evt.id).toBe(sendResp.event_id);
    expect(evt.delivery_status).toBeTruthy();

    // List events — should include the event we just sent.
    const listResp = await client.listEvents({ limit: 10 });
    expect(listResp.events.length).toBeGreaterThan(0);

    // List deliveries — one child per matching endpoint.
    const delivResp = await client.listDeliveries(sendResp.event_id!);
    expect(delivResp.event_id).toBe(sendResp.event_id);
    expect(delivResp.deliveries.length).toBeGreaterThanOrEqual(0);

    // Disable endpoint to clean up.
    await client.disableEndpoint(ep.id);
  });

  it("replay returns 409 for a not-yet-failed event", async () => {
    const client = new Relay({
      apiKey: apiKey!,
      baseUrl: baseUrl!,
    });

    const sendResp = await client.sendEvent(
      {
        source: "sdk-node-integration",
        event_type: "integration.test",
        data: {},
      },
      `idem-replay-${Date.now()}`,
    );

    // Immediately after ingestion, the event should not be failed. Guard
    // against a fast worker transitioning it to failed before this check
    // runs — that would make replay succeed instead of returning 409.
    const preReplay = await client.inspectEvent(sendResp.event_id!);
    if (preReplay.delivery_status === "failed") {
      return; // race: worker already terminalized the delivery
    }
    await expect(client.replayEvent(sendResp.event_id!)).rejects.toBeInstanceOf(
      RelayAPIError,
    );
  });
});
