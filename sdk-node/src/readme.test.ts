import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

const readme = readFileSync(new URL("../README.md", import.meta.url), "utf8");

it("documents endpoint, health, replay, and delivery SDK examples", () => {
  for (const example of [
    "relay.listEndpoints()",
    "relay.createEndpoint(",
    "relay.updateEndpoint(",
    "relay.getEndpointHealth(",
    "relay.replayEvent(",
    "relay.listDeliveries(",
    "../docs/swagger.yaml",
  ]) {
    expect(readme).toContain(example);
  }
  expect(readme).toContain('"last_success_at": null');
  expect(readme).toContain('"last_failure_at": null');
  expect(readme).toContain('"p50_latency_ms": null');
  expect(readme).toContain('"p95_latency_ms": null');
  expect(readme).toContain('"p99_latency_ms": null');
});
