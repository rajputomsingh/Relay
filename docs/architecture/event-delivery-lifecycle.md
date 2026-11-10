# Relay Event Delivery Lifecycle Contract

**Status:** Proposed  
**Scope:** Event ingestion, fan-out, delivery execution, aggregation, replay, and recovery  
**Applies to:** Relay core delivery engine

---

## 1. Purpose

This document defines the authoritative lifecycle of events and their deliveries in Relay.

The objective is to guarantee consistent event state, reliable fan-out, recoverable delivery execution, and predictable replay behavior across normal operation, failures, and concurrent workers.

Every reliability refactor must preserve the invariants defined here.

## 2. Core Design Principles

1. Event ingestion and delivery execution are separate lifecycle stages.
2. An event can produce multiple endpoint-specific deliveries.
3. Each event-endpoint pair must have at most one logical delivery record.
4. Delivery execution follows at-least-once semantics.
5. Workers must safely recover deliveries abandoned by expired leases.
6. Parent event status must be derived from child delivery outcomes.
7. Replay must preserve the original event identity.
8. Recovery operations must be idempotent.
9. Existing API compatibility must be preserved unless an intentional versioned change is approved.

## 3. Event Lifecycle

An event represents an accepted incoming payload.

The event lifecycle must distinguish:

- Acceptance: the event has been successfully persisted.
- Fan-out: eligible endpoints are evaluated and delivery records are created.
- Processing: endpoint-specific deliveries are claimed by workers.
- Completion: delivery outcomes have been aggregated into the parent event state.

### Zero-destination behavior

An event with no eligible destination must not remain indefinitely in an ambiguous accepted or processing state.

The system must represent this outcome explicitly using:

`no_destination`

This state must distinguish an event with no matching endpoints from an event whose deliveries failed.

A regression test must verify the zero-destination path.

## 4. Delivery Identity and Idempotency

A delivery represents the relationship between one event and one endpoint.

The logical identity of a delivery is:

`(event_id, endpoint_id)`

The database must enforce uniqueness for this relationship.

Repeated fan-out operations must not create duplicate logical deliveries.

The system must tolerate retries, worker restarts, and repeated processing without creating duplicate delivery records.

## 5. Parent Event Status Aggregation

Parent event status must be derived from the outcomes of its child deliveries.

The aggregation layer must be the sole authoritative writer of derived parent lifecycle status.

Individual workers must not independently calculate and overwrite parent event status.

### Required outcomes

- All eligible deliveries succeed: parent event becomes `delivered`.
- One or more deliveries fail permanently: parent event becomes `failed`.
- Deliveries remain eligible for execution or retry: parent event remains non-terminal.
- No eligible destinations: parent event becomes `no_destination`.

### Mixed outcomes

When an event has multiple deliveries and at least one delivery permanently fails, the parent event must become `failed`, even if other deliveries succeeded.

A successful child delivery must not incorrectly mark the parent event as delivered while another child has failed.

Aggregation must be safe under concurrent updates and repeated execution.

## 6. Fan-out Completeness

Fan-out must reliably create one delivery for every eligible endpoint.

The system must distinguish:

- Fan-out not started.
- Fan-out in progress.
- Fan-out completed.
- Fan-out incomplete or interrupted.

The expected delivery count must be maintained consistently with the fan-out operation.

A partially completed fan-out must be detectable and recoverable.

Repeated fan-out must be idempotent and must not create duplicate deliveries.

A regression test must cover interruption during fan-out and subsequent recovery.

## 7. Delivery Leases and Worker Recovery

Workers claim deliveries using leases.

A claimed delivery must have sufficient ownership information to prevent stale workers from overwriting newer execution results.

### Lease invariants

1. A delivery may be claimed only when it is eligible for processing.
2. A processing delivery with an active lease must not be reclaimed.
3. An expired lease must be recoverable.
4. Lease recovery must be atomic.
5. Lease ownership must be validated before a worker updates delivery state.
6. Lease fields must be cleared when a delivery is reset to pending.

### Atomic recovery

Recovery of an expired processing delivery must use a conditional database update that verifies:

- The delivery identifier.
- The current processing status.
- The lease expiration time is less than or equal to the recovery time.

The recovery operation must atomically:

- Reset the delivery to `pending`.
- Remove `lease_token`.
- Remove `lease_until`.

The system must not use a separate read-then-update sequence that allows concurrent workers to recover the same delivery incorrectly.

### Timestamp correctness

Orphaned event detection must use the timestamp actually written during event ingestion.

In particular, queries must use `received_at` when that is the persisted ingestion timestamp, rather than assuming `created_at` exists.

Regression tests must verify timestamp correctness and concurrent lease recovery.

## 8. Replay Semantics

Replay must requeue eligible failed deliveries without changing the identity of the original event.

Replay must:

1. Preserve the original `event_id`.
2. Preserve the original endpoint relationship.
3. Requeue the intended failed deliveries.
4. Avoid creating duplicate logical deliveries.
5. Preserve delivery attempt history according to the existing data model.
6. Allow normal worker execution and status aggregation to process replayed deliveries.

Replay must not silently create a new event to represent the same original event.

Tests must verify actual delivery requeue behavior rather than relying only on mocked or no-op fan-out implementations.

## 9. Reconciler Responsibilities

The reconciler is responsible for repairing lifecycle inconsistencies that can arise from interrupted operations, worker crashes, and partial processing.

It must be able to:

- Detect orphaned events.
- Detect incomplete fan-out.
- Identify expired delivery leases.
- Recover abandoned processing deliveries.
- Repair stale parent event states through the authoritative aggregation path.
- Execute recovery operations repeatedly without producing duplicate records or inconsistent state.

Reconciliation must be idempotent.

It must not overwrite active worker ownership or convert valid in-progress work into an incorrect terminal state.

Regression tests must cover stale parent recovery and orphan detection.

## 10. Required Regression Coverage

The reliability refactor must add or strengthen tests for the following scenarios:

| Area                   | Required behavior                                           |
| ---------------------- | ----------------------------------------------------------- |
| Zero destinations      | Parent transitions to `no_destination`                      |
| Successful aggregation | All successful children produce `delivered`                 |
| Mixed outcomes         | A permanent child failure produces parent `failed`          |
| Partial fan-out        | Interrupted fan-out can be detected and recovered           |
| Duplicate fan-out      | No duplicate event-endpoint deliveries                      |
| Active lease           | Active processing lease is not reclaimed                    |
| Expired lease          | Expired processing lease is atomically recovered            |
| Lease ownership        | Stale lease token cannot overwrite newer state              |
| Orphan detection       | Ingestion timestamp is used correctly                       |
| Reconciliation         | Stale parent status is repaired                             |
| Replay                 | Failed deliveries are requeued with original event identity |
| Concurrent workers     | Concurrent claims do not produce duplicate ownership        |
| MongoDB outage         | Worker recovers after database availability returns         |

Existing passing tests must continue to pass.

## 11. Database Integrity

MongoDB indexes and uniqueness constraints are part of the reliability contract.

The implementation must verify the actual provisioned indexes, not only their declarations in application code.

At minimum, verify:

- Unique event-endpoint delivery identity.
- Endpoint uniqueness constraints.
- Delivery scheduling index.
- Delivery lease-expiration index.
- Delivery endpoint/status index.
- Delivery event lookup index.
- Delivery attempt lookup indexes.
- Event idempotency-key uniqueness.

Index names and exact key definitions must be verified against the deployed database before finalizing the baseline.

## 12. Compatibility Requirements

The refactor must preserve existing public API behavior unless an intentional versioned change is approved.

The following must remain compatible:

- Existing event ingestion contracts.
- Existing delivery endpoints.
- Existing SDK request and response expectations.
- Existing authentication behavior.
- Existing persisted event and delivery identity.

Internal lifecycle changes must not silently break existing clients.

## 13. Baseline and Acceptance Requirements

Before lifecycle implementation begins, the baseline PR must record:

1. The exact source commit used for testing.
2. Go version.
3. Docker and Docker Compose versions.
4. MongoDB version and image.
5. Test environment configuration.
6. Full test output.
7. Expected skipped tests and their reasons.
8. Actual MongoDB index inventory.

The baseline must be reproducible against the recorded source commit.

Every implementation PR must include:

- The invariant it establishes or restores.
- A regression test.
- Relevant test results.
- Compatibility considerations.
- A concise explanation of the implementation.

No lifecycle implementation PR should proceed without the baseline and contract being reviewed.

## 14. Implementation Sequence

The proposed implementation order is:

1. `chore/reliability-baseline`
2. `fix/reconciler-recovery`
3. `fix/fanout-recovery`
4. `refactor/event-lifecycle`
5. `fix/replay-deliveries`
6. `test/failure-recovery`
7. `security/production-hardening`
8. `fix/sdk-contracts`
9. `release/stable`

Each PR must remain focused on its intended scope.

Dependencies between phases must be respected.

## 15. Definition of Done

The Relay event-delivery lifecycle refactor is complete when:

- Every event reaches an explicit, consistent lifecycle outcome.
- Zero-destination events are represented correctly.
- Fan-out is complete, idempotent, and recoverable.
- Parent status is derived through one authoritative aggregation path.
- Mixed delivery outcomes are handled correctly.
- Expired leases are recovered atomically.
- Stale workers cannot overwrite newer ownership.
- Replay preserves event identity and requeues the intended deliveries.
- Reconciliation repairs stale state safely.
- Required regression tests pass.
- Existing API compatibility is preserved.
- Database indexes and uniqueness constraints are verified.
- The complete project test suite passes.

---

**Engineering rule**

Every PR in the Relay Reliability Refactor must establish or restore a specific lifecycle invariant, include regression coverage, and preserve existing API compatibility unless an intentional versioned change is approved.
