package relay

import (
	"context"
	"fmt"
	"net/http"
)

// Event represents an event submission payload.
type Event struct {
	Source    string                 `json:"source"`
	EventType string                 `json:"event_type"`
	Data      map[string]interface{} `json:"data"`
}

// EventResponse is the API response returned when an event is created.
type EventResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// SendEvent sends an event to Relay.
// If idempotencyKey is provided, it will be sent as the Idempotency-Key header.
func (c *Client) SendEvent(ctx context.Context, event Event, idempotencyKey string) (*EventResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/events", event)
	if err != nil {
		return nil, err
	}

	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	var resp EventResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("send event: %w", err)
	}

	return &resp, nil
}
