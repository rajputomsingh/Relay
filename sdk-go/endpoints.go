package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// EventFilter selects which events an endpoint receives.
// An empty filter matches all events.
type EventFilter struct {
	Sources    []string `json:"sources,omitempty"`
	EventTypes []string `json:"event_types,omitempty"`
}

// Endpoint is a registered webhook destination as returned by the API.
//
// The SigningSecret is never returned by the API; the HasSigningSecret
// field indicates whether the endpoint was created with a signing secret.
type Endpoint struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	URL              string      `json:"url"`
	HasSigningSecret bool        `json:"has_signing_secret"`
	Enabled          bool        `json:"enabled"`
	EventFilter      EventFilter `json:"event_filter,omitempty"`
	MaxAttempts      int         `json:"max_attempts"`
	RateLimitRPS     int         `json:"rate_limit_rps,omitempty"`
	CreatedAt        string      `json:"created_at"`
	UpdatedAt        string      `json:"updated_at"`
	DisabledAt       string      `json:"disabled_at,omitempty"`
}

// CreateEndpointParams is the input for CreateEndpoint.
type CreateEndpointParams struct {
	// Name is required and must be unique among endpoints.
	Name string
	// URL must be an absolute HTTP(S) URL. Private/loopback/link-local
	// literal IPs are rejected by the server.
	URL string
	// SigningSecret, if set, must be >= 32 characters. Cannot be read
	// back from the API after creation.
	SigningSecret string
	// EventFilter restricts which events this endpoint receives.
	EventFilter EventFilter
	// MaxAttempts must be 1-20.
	MaxAttempts int
	// RateLimitRPS is 0 (unlimited) or 1-1000.
	RateLimitRPS int
	// Enabled defaults to true if not set explicitly on the server side.
	// To create a disabled endpoint, set Enabled=true here and disable
	// it after creation, or use the pointer form in future API versions.
	Enabled bool
}

type createEndpointBody struct {
	Name          string      `json:"name"`
	URL           string      `json:"url"`
	SigningSecret string      `json:"signing_secret,omitempty"`
	EventFilter   EventFilter `json:"event_filter"`
	MaxAttempts   int         `json:"max_attempts"`
	RateLimitRPS  int         `json:"rate_limit_rps"`
	Enabled       bool        `json:"enabled"`
}

// CreateEndpoint registers a new webhook destination.
func (c *Client) CreateEndpoint(
	ctx context.Context,
	params CreateEndpointParams,
) (*Endpoint, error) {
	body := createEndpointBody{
		Name:          params.Name,
		URL:           params.URL,
		SigningSecret: params.SigningSecret,
		EventFilter:   params.EventFilter,
		MaxAttempts:   params.MaxAttempts,
		RateLimitRPS:  params.RateLimitRPS,
		Enabled:       params.Enabled,
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/v1/endpoints", body)
	if err != nil {
		return nil, err
	}

	var resp Endpoint
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("create endpoint: %w", err)
	}
	return &resp, nil
}

// ListEndpointsResponse is the response returned by GET /v1/endpoints.
type ListEndpointsResponse struct {
	Endpoints []Endpoint `json:"endpoints"`
	Count     int        `json:"count"`
}

// ListEndpoints returns all endpoints (enabled and disabled).
func (c *Client) ListEndpoints(
	ctx context.Context,
) (*ListEndpointsResponse, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/endpoints", nil)
	if err != nil {
		return nil, err
	}

	var resp ListEndpointsResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	return &resp, nil
}

// GetEndpoint returns a single endpoint by ID.
func (c *Client) GetEndpoint(
	ctx context.Context,
	endpointID string,
) (*Endpoint, error) {
	if endpointID == "" {
		return nil, fmt.Errorf("get endpoint: endpointID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/v1/endpoints/"+url.PathEscape(endpointID),
		nil,
	)
	if err != nil {
		return nil, err
	}

	var resp Endpoint
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("get endpoint: %w", err)
	}
	return &resp, nil
}

// UpdateEndpointParams is the input for UpdateEndpoint.
// Nil pointer fields are left unchanged on the server.
type UpdateEndpointParams struct {
	Name          *string
	URL           *string
	SigningSecret *string
	EventFilter   *EventFilter
	MaxAttempts   *int
	RateLimitRPS  *int
	Enabled       *bool
}

type updateEndpointBody struct {
	Name          *string      `json:"name,omitempty"`
	URL           *string      `json:"url,omitempty"`
	SigningSecret *string      `json:"signing_secret,omitempty"`
	EventFilter   *EventFilter `json:"event_filter,omitempty"`
	MaxAttempts   *int         `json:"max_attempts,omitempty"`
	RateLimitRPS  *int         `json:"rate_limit_rps,omitempty"`
	Enabled       *bool        `json:"enabled,omitempty"`
}

// UpdateEndpoint applies a partial update to an endpoint.
func (c *Client) UpdateEndpoint(
	ctx context.Context,
	endpointID string,
	params UpdateEndpointParams,
) (*Endpoint, error) {
	if endpointID == "" {
		return nil, fmt.Errorf("update endpoint: endpointID is required")
	}

	body := updateEndpointBody{
		Name:          params.Name,
		URL:           params.URL,
		SigningSecret: params.SigningSecret,
		EventFilter:   params.EventFilter,
		MaxAttempts:   params.MaxAttempts,
		RateLimitRPS:  params.RateLimitRPS,
		Enabled:       params.Enabled,
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPatch,
		"/v1/endpoints/"+url.PathEscape(endpointID),
		body,
	)
	if err != nil {
		return nil, err
	}

	var resp Endpoint
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("update endpoint: %w", err)
	}
	return &resp, nil
}

// DisableEndpoint soft-deletes an endpoint by setting Enabled=false.
//
// The endpoint is not deleted from the database. Deliveries for the
// endpoint that have not yet been attempted will fail with
// "endpoint disabled" on their next attempt.
func (c *Client) DisableEndpoint(
	ctx context.Context,
	endpointID string,
) (*Endpoint, error) {
	if endpointID == "" {
		return nil, fmt.Errorf("disable endpoint: endpointID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodDelete,
		"/v1/endpoints/"+url.PathEscape(endpointID),
		nil,
	)
	if err != nil {
		return nil, err
	}

	var resp Endpoint
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("disable endpoint: %w", err)
	}
	return &resp, nil
}
