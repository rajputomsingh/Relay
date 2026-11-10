package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Event represents an event submission payload.
type Event struct {
	Source    string                 `json:"source"`
	EventType string                 `json:"event_type"`
	Data      map[string]interface{} `json:"data"`
}

// EventResponse is the response returned by POST /v1/events.
//
// On first submission the server returns status "accepted" with an
// EventID. On idempotent retry (same Idempotency-Key already used) the
// server returns status "already_accepted" with IdempotencyKey set but
// EventID empty — the original event ID is not returned on the retry
// path. Callers who need the ID on retry must query it separately.
type EventResponse struct {
	Status         string `json:"status"`
	EventID        string `json:"event_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// EventInspection is the response returned by GET /v1/events/{id}.
//
// It includes the parent event's lifecycle state and metadata.
type EventInspection struct {
	ID               string                 `json:"id"`
	Source           string                 `json:"source"`
	EventType        string                 `json:"event_type"`
	Data             map[string]interface{} `json:"data"`
	IdempotencyKey   string                 `json:"idempotency_key"`
	ReceivedAt       string                 `json:"received_at"`
	Status           string                 `json:"status"`
	DeliveryStatus   string                 `json:"delivery_status"`
	DeliveryAttempts int                    `json:"delivery_attempts"`
	DeliveredAt      string                 `json:"delivered_at,omitempty"`
	FailedAt         string                 `json:"failed_at,omitempty"`
	ReplayCount      int                    `json:"replay_count,omitempty"`
	LastReplayedAt   string                 `json:"last_replayed_at,omitempty"`
}

// ReplayResponse is the response returned by POST /v1/events/{id}/replay.
type ReplayResponse struct {
	Status             string `json:"status"`
	EventID            string `json:"event_id"`
	IdempotencyKey     string `json:"idempotency_key"`
	DeliveriesRequeued int    `json:"deliveries_requeued"`
}

// SendEvent submits an event for delivery.
//
// If idempotencyKey is empty, no Idempotency-Key header is sent and the
// server will reject the request with HTTP 400. Passing an empty
// idempotencyKey is not a supported usage.
//
// The returned EventResponse distinguishes first submission from idempotent
// retry via the Status field: "accepted" on first submission,
// "already_accepted" on retry. The EventID field is only populated on
// first submission.
func (c *Client) SendEvent(
	ctx context.Context,
	event Event,
	idempotencyKey string,
) (*EventResponse, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("send event: idempotencyKey is required")
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/v1/events", event)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Idempotency-Key", idempotencyKey)

	var resp EventResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("send event: %w", err)
	}
	return &resp, nil
}

// InspectEvent returns the current state of an event by ID.
func (c *Client) InspectEvent(
	ctx context.Context,
	eventID string,
) (*EventInspection, error) {
	if eventID == "" {
		return nil, fmt.Errorf("inspect event: eventID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/v1/events/"+url.PathEscape(eventID),
		nil,
	)
	if err != nil {
		return nil, err
	}

	var resp EventInspection
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("inspect event: %w", err)
	}
	return &resp, nil
}

// ReplayEvent requeues the failed deliveries for a failed event.
//
// Returns HTTP 409 (surfaced as APIError) if the event is not in `failed`
// state, or if the event has no failed deliveries.
func (c *Client) ReplayEvent(
	ctx context.Context,
	eventID string,
) (*ReplayResponse, error) {
	if eventID == "" {
		return nil, fmt.Errorf("replay event: eventID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPost,
		"/v1/events/"+url.PathEscape(eventID)+"/replay",
		nil,
	)
	if err != nil {
		return nil, err
	}

	var resp ReplayResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("replay event: %w", err)
	}
	return &resp, nil
}

// ListEventsResponse is the response returned by GET /v1/events.
type ListEventsResponse struct {
	Events     []EventInspection `json:"events"`
	Limit      int               `json:"limit"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ListEventsParams controls optional filters for ListEvents.
type ListEventsParams struct {
	// DeliveryStatus filters events by parent delivery_status.
	// Valid values: pending, processing, delivered, failed, disabled.
	// Empty string means no filter.
	DeliveryStatus string
	// Limit is the page size. 1-100. Zero means server default (50).
	Limit int
	// Cursor is the pagination cursor from a previous response's NextCursor.
	Cursor string
}

// ListEvents returns a paginated list of events.
func (c *Client) ListEvents(
	ctx context.Context,
	params ListEventsParams,
) (*ListEventsResponse, error) {
	q := url.Values{}
	if params.DeliveryStatus != "" {
		q.Set("delivery_status", params.DeliveryStatus)
	}
	if params.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", params.Limit))
	}
	if params.Cursor != "" {
		q.Set("cursor", params.Cursor)
	}

	path := "/v1/events"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp ListEventsResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return &resp, nil
}
