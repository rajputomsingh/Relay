# RFC 0001: Multi-Tenant Isolation

## Executive Summary
Relay currently assumes a single-tenant deployment per instance. All endpoints, events, and delivery records share a single MongoDB database without a tenant boundary. This RFC evaluates strategies for introducing multi-tenancy to support a hosted Relay Cloud and enterprise on-premise deployments.

## Context
As Relay scales from a self-hosted tool to a hosted infrastructure service, the lack of tenant isolation is the single biggest blocker. Without it:
1. Two different applications cannot safely share a Relay instance.
2. Data leakage between customers is a critical security risk.
3. Noisy neighbors can exhaust resources (concurrency, queue depth) for everyone.

## Goals
- Add a `tenant_id` to every core entity.
- Ensure no cross-tenant data access is possible via the API or the delivery worker.
- Provide a path to migrate existing single-tenant deployments without downtime.
## Isolation Models

We have four primary options for database isolation. 

### Option A: Shared Collections with `tenant_id` (Recommended)
Every document gets a `tenant_id` field. The API middleware enforces that the `tenant_id` in the JWT/API key matches the query.

- **Pros:** Extremely simple to query, minimal infrastructure overhead, easy to scale.
- **Cons:** Requires strict discipline to ensure every single database query includes `tenant_id`. A missing `WHERE` clause means a data leak.

### Option B: Database-per-Tenant
Each tenant gets their own MongoDB database (e.g., `relay_tenant_123`).

- **Pros:** Absolute data isolation. A query bug cannot leak data between tenants.
- **Cons:** Massive operational overhead. Connection pooling becomes a nightmare if you have 1,000 tenants.

### Option C: Collection-per-Tenant
All tenants share a database, but each gets a collection (e.g., `events_tenant_123`).

- **Pros:** Better isolation than shared collections.
- **Cons:** MongoDB has limits on collections, and it makes migrations across tenants complex.

## Decision
**We will use Option A (Shared Collections with `tenant_id`).** 
Relay is built for scale. Option A is the industry standard (used by Stripe, GitHub, etc.) for good reason. The isolation safety will be enforced at the Go application layer via a strict repository pattern, not at the database layer.
