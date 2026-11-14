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

## Authentication and Tenant Resolution

If we are adding a `tenant_id`, how does it get into the request?

### 1. API Keys
Currently, Relay uses `RELAY_API_KEY`. We will introduce API Keys tied directly to a tenant.
- The `api_keys` collection will have: `key`, `tenant_id`, `created_at`.
- When a request hits `/v1/events`, the middleware extracts the Bearer token, looks up the `tenant_id`, and injects it into the `context.Context`.

### 2. The Tenant Context
We will never trust a `tenant_id` provided in the JSON body. It must only come from the API key. 
In Go, we will create a helper: `tenantID := tenant.FromContext(ctx)`.

## Data Model and Indexing

We are adding `tenant_id` as a BSON string to:

- `events`
- `endpoints`
- `deliveries`
- `attempts`

### Index Updates

Every tenant-scoped index must include `tenant_id` as its leading key.

**Before:**

```go
index := mongo.IndexModel{
    Keys: bson.D{{Key: "event_type", Value: 1}},
}
```

**After:**

```go
index := mongo.IndexModel{
    Keys: bson.D{
        {Key: "tenant_id", Value: 1},
        {Key: "event_type", Value: 1},
    },
}
```

Tenant-prefixed compound indexes allow MongoDB to efficiently execute tenant-scoped queries.

Existing indexes must be audited before migration. Unique indexes must also be reviewed to determine whether uniqueness should be global or tenant-scoped.


## Query Impact and Enforcement

The biggest risk of shared collections is accidentally executing an unscoped query.

For example:

```go
db.Collection("events").Find(ctx, bson.D{})
```

To prevent this, database access will be centralized behind tenant-aware repository interfaces.

The database driver will not be exposed directly to HTTP handlers.

### Repository Interface

```go
type EventRepository interface {
    FindByID(
        ctx context.Context,
        tenantID string,
        id string,
    ) (*Event, error)
}
```

### Tenant-Scoped Query

```go
filter := bson.D{
    {Key: "_id", Value: id},
    {Key: "tenant_id", Value: tenantID},
}
```

All tenant-owned reads, updates, deletes, and aggregation pipelines must enforce the same isolation rule.

Repository implementations must reject operations without a valid tenant identity.

Automated tests and code review will verify cross-tenant isolation.

The repository pattern reduces accidental omissions but is not a compile-time guarantee. Enforcement must also be supported by tests, code review, and restricted database access.


## Worker and Delivery Flow

The delivery workers currently poll the `deliveries` collection.

With multi-tenancy, workers will process deliveries belonging to multiple tenants while preserving tenant isolation.

### Delivery Processing

1. The worker claims a delivery.
2. The delivery document contains its `tenant_id`.
3. The worker fetches endpoint configuration using both the endpoint ID and delivery tenant ID.
4. The webhook signing secret is resolved within the same tenant boundary.
5. The worker executes the delivery and records attempts under the same tenant.

### Tenant Ownership Validation

Endpoint lookups must include tenant ownership:

```go
filter := bson.D{
    {Key: "_id", Value: delivery.EndpointID},
    {Key: "tenant_id", Value: delivery.TenantID},
}
```

The worker must never trust an endpoint ID alone.

### Cross-Tenant Retries

Lease recovery must remain independent of tenant identity.

If a worker crashes:

1. The lease expires.
2. The reconciler identifies the expired lease.
3. The delivery is reclaimed.
4. The original `tenant_id` is preserved.
5. The delivery is re-queued for processing.

Tenant ownership must remain unchanged throughout retries, lease recovery, and dead-letter handling.

