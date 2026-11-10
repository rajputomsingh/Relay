package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultBaseURL is the default Relay API endpoint.
const DefaultBaseURL = "http://localhost:8080"

// Client is the main entry point for interacting with the Relay API.
type Client struct {
	baseURL     string
	apiKey      string
	httpClient  *http.Client
	retryConfig RetryConfig
}

// Option allows configuring the Client.
type Option func(*Client)

// WithBaseURL overrides the default Relay API URL.
func WithBaseURL(url string) Option {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient creates a new Relay API client.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		retryConfig: DefaultRetryConfig,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// newRequest builds an HTTP request with standard headers.
func (c *Client) newRequest(ctx context.Context, method, path string, body interface{}) (*http.Request, error) {
	u := fmt.Sprintf("%s%s", c.baseURL, path)

	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		buf = bytes.NewBuffer(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Add this to support retries:
	if body != nil {
		b, _ := json.Marshal(body)
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewBuffer(b)), nil
		}
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "relay-go/0.2.0")

	return req, nil
}

// do executes the request, handling retries and decoding the response.
func (c *Client) do(req *http.Request, v interface{}) error {
	var lastErr error

	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		// We need to re-read the body on retries, so we clone the request
		clonedReq := req.Clone(req.Context())
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return fmt.Errorf("clone request body: %w", err)
			}
			clonedReq.Body = body
		}

		resp, err := c.httpClient.Do(clonedReq)
		if err != nil {
			lastErr = fmt.Errorf("execute request: %w", err)
			// Network error, wait and retry
			time.Sleep(c.retryConfig.backoff(attempt))
			continue
		}

		// Check if we need to retry based on status code
		if isRetryableStatus(resp.StatusCode) && attempt < c.retryConfig.MaxRetries {
			resp.Body.Close() // Close body to reuse connection
			lastErr = fmt.Errorf("retryable status: %d", resp.StatusCode)
			time.Sleep(c.retryConfig.backoff(attempt))
			continue
		}

		// If it's a non-retryable error, or success, process and return
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return parseError(resp)
		}

		if v != nil {
			if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}

		return nil
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
