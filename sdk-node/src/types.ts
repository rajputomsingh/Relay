// Types matching the Relay server's actual API responses.
//
// Field names and nullability mirror the Go structs in internal/handlers/.
// Do not rename these fields; SDK consumers depend on the exact keys.

// ---------- Events ----------

export interface EventPayload {
  source: string;
  event_type: string;
  data: Record<string, unknown>;
}

/**
 * Response from POST /v1/events.
 *
 * On first submission: status="accepted", event_id is populated.
 * On idempotent retry: status="already_accepted", event_id is empty —
 *   the server does not return the original event ID on the retry path.
 *
 * Callers must check `status` before using `event_id`.
 */
export interface EventResponse {
  status: "accepted" | "already_accepted";
  event_id?: string;
  idempotency_key: string;
}

export interface EventInspection {
  id: string;
  source: string;
  event_type: string;
  data: Record<string, unknown>;
  idempotency_key: string;
  received_at: string;
  status: string;
  delivery_status: string;
  delivery_attempts: number;
  delivered_at?: string;
  failed_at?: string;
  replay_count?: number;
  last_replayed_at?: string;
}

export interface ListEventsParams {
  delivery_status?:
    | "pending"
    | "processing"
    | "delivered"
    | "failed"
    | "disabled";
  limit?: number; // 1-100, default 50
  cursor?: string;
}

export interface ListEventsResponse {
  events: EventInspection[];
  limit: number;
  has_more: boolean;
  next_cursor?: string;
}

export interface ReplayResponse {
  status: "replay_queued";
  event_id: string;
  idempotency_key: string;
  deliveries_requeued: number;
}

// ---------- Deliveries ----------

export interface DeliveryState {
  endpoint_id: string;
  status: "pending" | "processing" | "delivered" | "failed";
  attempts: number;
  max_attempts: number;
  next_attempt_at?: string;
  delivered_at?: string;
  failed_at?: string;
  last_error?: string;
  updated_at: string;
}

export interface ListDeliveriesResponse {
  event_id: string;
  deliveries: DeliveryState[];
}

// ---------- Attempts ----------

export interface Attempt {
  id: string;
  event_id: string;
  endpoint_id?: string;
  attempt_number: number;
  started_at: string;
  finished_at: string;
  duration_ms: number;
  http_status?: number;
  outcome: "delivered" | "failed";
}

export interface ListAttemptsParams {
  limit?: number; // 1-100, default 50
  cursor?: string;
}

export interface ListAttemptsResponse {
  event_id: string;
  attempts: Attempt[];
  limit: number;
  has_more: boolean;
  next_cursor?: string;
}

// ---------- Endpoints ----------

export interface EventFilter {
  sources?: string[];
  event_types?: string[];
}

export interface Endpoint {
  id: string;
  name: string;
  url: string;
  has_signing_secret: boolean;
  enabled: boolean;
  event_filter?: EventFilter;
  max_attempts: number;
  rate_limit_rps?: number;
  created_at: string;
  updated_at: string;
  disabled_at?: string;
}

export interface CreateEndpointParams {
  name: string;
  url: string;
  signing_secret?: string;
  event_filter?: EventFilter;
  max_attempts?: number;
  rate_limit_rps?: number;
  enabled?: boolean;
}

export interface UpdateEndpointParams {
  name?: string;
  url?: string;
  signing_secret?: string;
  event_filter?: EventFilter;
  max_attempts?: number;
  rate_limit_rps?: number;
  enabled?: boolean;
}

export interface ListEndpointsResponse {
  endpoints: Endpoint[];
  count: number;
}

// ---------- Health ----------

export interface HealthResponse {
  service?: string;
  status: string;
  database?: string;
}

export interface EndpointHealthResponse {
  success_rate_1h: number;
  attempts_1h: number;
  successes_1h: number;
  failures_1h: number;
  consecutive_failures: number;
  last_success_at: string | null;
  last_failure_at: string | null;
}
