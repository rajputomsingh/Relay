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