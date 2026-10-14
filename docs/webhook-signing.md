# Webhook Signing Protocol

Relay signs outbound webhook requests using HMAC-SHA256 so receivers can verify that:

1. The request was produced by a service holding the shared secret.
2. The request body was not modified in transit.
3. The request is recent enough to be accepted.

Signing proves possession of the shared secret and integrity of the payload. It does **not** prevent duplicate delivery. Receivers should still deduplicate using `X-Relay-Event-ID` or the `idempotency_key` in the payload.

## Headers

| Header | Purpose |
|---|---|
| `X-Relay-Timestamp` | Unix timestamp (seconds, UTC) used to reject stale requests |
| `X-Relay-Signature` | `v1=<hex_hmac_sha256>` of the canonical message |
| `X-Relay-Event-ID` | Stable event identifier for receiver-side deduplication |

## Canonical Message

The signed message is the timestamp, followed by a period (`.`), followed by the exact request body bytes:

```text
<timestamp>.<exact-request-body-bytes>
```

The signature is calculated as:

```text
hex(HMAC_SHA256(secret, timestamp + "." + body))
```

The resulting signature header uses the `v1=` prefix:

```text
X-Relay-Signature: v1=<hex_hmac_sha256>
```

The body signed is the **exact byte sequence sent over HTTP**. Receivers **must** verify the signature against the raw request body, not a re-serialized JSON object. Re-serializing JSON can change whitespace, key ordering, or escaping and cause signature verification to fail.

## Timestamp Tolerance

Receivers should reject requests whose timestamp falls outside a bounded time window. A tolerance of **5 minutes** is recommended.

A clock-skew allowance is acceptable, provided the receiver enforces a finite acceptance window.

Timestamp validation helps limit replay attacks but does not completely prevent them. Receivers should combine timestamp validation with event deduplication.

## Verification Steps (Receiver)

Receivers should perform the following steps before processing an event:

1. Read the raw HTTP request body.
2. Parse `X-Relay-Timestamp`. Reject the request if the timestamp is missing, malformed, or outside the permitted time window.
3. Recompute `HMAC-SHA256(secret, timestamp + "." + body)` using the shared secret and raw request body.
4. Extract the signature from `X-Relay-Signature`, verify the `v1=` prefix, and compare the supplied hexadecimal digest with the expected digest using a constant-time comparison.
   - Go: `hmac.Equal`
   - Python: `hmac.compare_digest`
5. Reject the request if the signature is missing, malformed, or does not match.
6. Process the event only after signature verification succeeds.
7. Deduplicate the event using `X-Relay-Event-ID` or the `idempotency_key` in the payload.

Receivers must not process an event before signature verification succeeds.

## Backward Compatibility

Signing is optional.

When `RELAY_WEBHOOK_SIGNING_SECRET` is unset, Relay delivers unsigned requests exactly as before. This preserves compatibility with existing deployments and webhook consumers.

When `RELAY_WEBHOOK_SIGNING_SECRET` is set, every outbound delivery includes the signature headers:

- `X-Relay-Timestamp`
- `X-Relay-Signature`
- `X-Relay-Event-ID`

The event ID header supports receiver-side deduplication and is not itself part of the signed canonical message defined in this protocol.

## Secret Handling

- The signing secret is loaded from `RELAY_WEBHOOK_SIGNING_SECRET`.
- The secret must never be logged.
- The secret must never be returned by any API.
- The secret must never be included in delivery attempt history.
- The secret must not be committed to source control.
- Local development secrets should be stored in an untracked `.env` file or supplied through the deployment environment.
- `.env.example` should document the variable without containing a real secret.

### Secret Rotation

Rotating the secret changes the key used to generate and verify signatures.

To avoid delivery failures during rotation, coordinate the change between Relay and its receivers. Where supported, receivers can temporarily accept both the old and new secrets during a controlled transition. Once Relay uses the new secret and the transition is complete, remove the old secret from receiver configuration.

## Security Considerations

- Use HTTPS to protect webhook traffic in transit.
- Use a cryptographically strong, randomly generated secret.
- Keep the secret accessible only to authorized services and operators.
- Compare signatures using a constant-time comparison function.
- Validate timestamps before processing events.
- Deduplicate events because retries and replayed deliveries can produce duplicate requests.
- Reject invalid signatures before performing application-level side effects.

Webhook signing provides payload integrity and shared-secret authentication. It does not replace HTTPS, authorization, timestamp validation, or receiver-side idempotency.