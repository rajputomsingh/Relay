package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type cleanupTestTransport func(*http.Request) (*http.Response, error)

func (f cleanupTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type failingCloseBody struct {
	io.Reader
	err    error
	closes int
}

func (b *failingCloseBody) Close() error {
	b.closes++
	return b.err
}

func TestResponseCloseErrorPreservesOutcome(t *testing.T) {
	closeErr := errors.New("response cleanup failed")
	tests := []struct {
		name     string
		statuses []int
		body     string
		wantErr  string
		wantAPI  bool
	}{
		{name: "success", statuses: []int{200}, body: `{"id":"endpoint-id"}`},
		{name: "retry then success", statuses: []int{503, 200}, body: `{"id":"endpoint-id"}`},
		{name: "API error", statuses: []int{400}, body: `{"error":"invalid endpoint"}`, wantErr: "invalid endpoint", wantAPI: true},
		{name: "decode error", statuses: []int{200}, body: `{`, wantErr: "decode response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodies []*failingCloseBody
			transport := cleanupTestTransport(func(r *http.Request) (*http.Response, error) {
				attempt := len(bodies)
				if attempt >= len(tt.statuses) {
					t.Errorf("unexpected request after %d attempts", attempt)
					return nil, errors.New("unexpected extra request")
				}
				body := &failingCloseBody{Reader: strings.NewReader(tt.body), err: closeErr}
				bodies = append(bodies, body)
				return &http.Response{
					StatusCode: tt.statuses[attempt], Body: body,
					Header: make(http.Header), Request: r,
				}, nil
			})
			client := NewClient("test-key", WithHTTPClient(&http.Client{Transport: transport}))
			client.retryConfig = RetryConfig{MaxRetries: 1}
			got, err := client.GetEndpoint(context.Background(), "endpoint-id")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("GetEndpoint: %v", err)
				}
				if got.ID != "endpoint-id" {
					t.Errorf("endpoint ID = %q", got.ID)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("GetEndpoint error = %v; want %q", err, tt.wantErr)
			}
			if errors.Is(err, closeErr) {
				t.Errorf("cleanup error replaced request outcome: %v", err)
			}
			var apiErr *APIError
			if tt.wantAPI && !errors.As(err, &apiErr) {
				t.Errorf("GetEndpoint error = %T; want APIError", err)
			}
			if len(bodies) != len(tt.statuses) {
				t.Errorf("requests = %d; want %d", len(bodies), len(tt.statuses))
			}
			for i, body := range bodies {
				if body.closes != 1 {
					t.Errorf("response %d closed %d times; want 1", i, body.closes)
				}
			}
		})
	}
}
