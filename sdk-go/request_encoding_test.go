package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestEndpointRequestJSON(t *testing.T) {
	name, endpointURL, secret := "endpoint", "https://example.com/hook", "test-signing-secret"
	filter := EventFilter{Sources: []string{"test"}, EventTypes: []string{"created"}}
	maxAttempts, rateLimit, enabled := 3, 10, true
	empty, zero, disabled := "", 0, false
	emptyFilter := EventFilter{}

	tests := []struct {
		name   string
		create *CreateEndpointParams
		update *UpdateEndpointParams
		want   string
	}{
		{
			name:   "create includes zero values and omits empty secret",
			create: &CreateEndpointParams{},
			want:   `{"name":"","url":"","event_filter":{},"max_attempts":0,"rate_limit_rps":0,"enabled":false}`,
		},
		{
			name: "create includes all fields",
			create: &CreateEndpointParams{
				Name: name, URL: endpointURL, SigningSecret: secret, EventFilter: filter,
				MaxAttempts: maxAttempts, RateLimitRPS: rateLimit, Enabled: enabled,
			},
			want: `{"name":"endpoint","url":"https://example.com/hook","signing_secret":"test-signing-secret","event_filter":{"sources":["test"],"event_types":["created"]},"max_attempts":3,"rate_limit_rps":10,"enabled":true}`,
		},
		{
			name:   "update omits nil fields",
			update: &UpdateEndpointParams{},
			want:   `{}`,
		},
		{
			name: "update preserves explicit zero values",
			update: &UpdateEndpointParams{
				Name: &empty, URL: &empty, SigningSecret: &empty, EventFilter: &emptyFilter,
				MaxAttempts: &zero, RateLimitRPS: &zero, Enabled: &disabled,
			},
			want: `{"name":"","url":"","signing_secret":"","event_filter":{},"max_attempts":0,"rate_limit_rps":0,"enabled":false}`,
		},
		{
			name: "update includes all fields",
			update: &UpdateEndpointParams{
				Name: &name, URL: &endpointURL, SigningSecret: &secret, EventFilter: &filter,
				MaxAttempts: &maxAttempts, RateLimitRPS: &rateLimit, Enabled: &enabled,
			},
			want: `{"name":"endpoint","url":"https://example.com/hook","signing_secret":"test-signing-secret","event_filter":{"sources":["test"],"event_types":["created"]},"max_attempts":3,"rate_limit_rps":10,"enabled":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var want map[string]any
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path := http.MethodPost, "/v1/endpoints"
				if tt.update != nil {
					method, path = http.MethodPatch, "/v1/endpoints/endpoint-id"
				}
				if r.Method != method || r.URL.Path != path {
					t.Errorf("request = %s %s; want %s %s", r.Method, r.URL.Path, method, path)
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err)
				} else if !reflect.DeepEqual(got, want) {
					t.Errorf("request JSON = %#v; want %#v", got, want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"id":"endpoint-id"}`)
			}))
			t.Cleanup(server.Close)

			client := NewClient("test-key", WithBaseURL(server.URL))
			var err error
			if tt.create != nil {
				_, err = client.CreateEndpoint(context.Background(), *tt.create)
			} else {
				_, err = client.UpdateEndpoint(context.Background(), "endpoint-id", *tt.update)
			}
			if err != nil {
				t.Fatalf("endpoint request: %v", err)
			}
		})
	}
}
