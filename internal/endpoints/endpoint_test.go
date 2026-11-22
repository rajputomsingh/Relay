package endpoints

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEndpointValidate(t *testing.T) {
	valid := Endpoint{
		Name:        "billing",
		URL:         "https://example.com/hook",
		Enabled:     true,
		MaxAttempts: 8,
	}

	tests := []struct {
		name    string
		modify  func(*Endpoint)
		wantErr bool
	}{
		{
			name:   "valid endpoint",
			modify: func(*Endpoint) {},
		},
		{
			name:    "missing name",
			modify:  func(e *Endpoint) { e.Name = "" },
			wantErr: true,
		},
		{
			name:    "missing URL",
			modify:  func(e *Endpoint) { e.URL = "" },
			wantErr: true,
		},
		{
			name:    "non-absolute URL",
			modify:  func(e *Endpoint) { e.URL = "/hook" },
			wantErr: true,
		},
		{
			name:    "URL with user info",
			modify:  func(e *Endpoint) { e.URL = "https://u:p@example.com/hook" },
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			modify:  func(e *Endpoint) { e.URL = "ftp://example.com/hook" },
			wantErr: true,
		},
		{
			name:    "max attempts zero",
			modify:  func(e *Endpoint) { e.MaxAttempts = 0 },
			wantErr: true,
		},
		{
			name:    "max attempts too high",
			modify:  func(e *Endpoint) { e.MaxAttempts = 21 },
			wantErr: true,
		},
		{
			name:    "signing secret too short",
			modify:  func(e *Endpoint) { e.SigningSecret = "short" },
			wantErr: true,
		},
		{
			name: "valid signing secret",
			modify: func(e *Endpoint) {
				e.SigningSecret = "0123456789abcdef0123456789abcdef"
			},
		},
		{
			name:    "SSRF: loopback IPv4",
			modify:  func(e *Endpoint) { e.URL = "https://127.0.0.1/hook" },
			wantErr: true,
		},
		{
			name:    "SSRF: IPv6 loopback",
			modify:  func(e *Endpoint) { e.URL = "https://[::1]/hook" },
			wantErr: true,
		},
		{
			name:    "SSRF: RFC1918 10.x",
			modify:  func(e *Endpoint) { e.URL = "https://10.0.0.5/hook" },
			wantErr: true,
		},
		{
			name:    "SSRF: RFC1918 172.16.x",
			modify:  func(e *Endpoint) { e.URL = "https://172.16.0.1/hook" },
			wantErr: true,
		},
		{
			name:    "SSRF: RFC1918 192.168.x",
			modify:  func(e *Endpoint) { e.URL = "https://192.168.1.1/hook" },
			wantErr: true,
		},
		{
			name:    "SSRF: link-local (cloud metadata)",
			modify:  func(e *Endpoint) { e.URL = "https://169.254.169.254/hook" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.modify(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf(
					"Validate() error = %v, wantErr %v",
					err,
					tt.wantErr,
				)
			}
		})
	}
}

func TestEventFilterMatches(t *testing.T) {
	tests := []struct {
		name      string
		filter    EventFilter
		source    string
		eventType string
		want      bool
	}{
		{
			name:      "empty filter matches everything",
			filter:    EventFilter{},
			source:    "billing",
			eventType: "invoice.paid",
			want:      true,
		},
		{
			name: "source match",
			filter: EventFilter{
				Sources: []string{"billing", "orders"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      true,
		},
		{
			name: "source no match",
			filter: EventFilter{
				Sources: []string{"billing"},
			},
			source:    "orders",
			eventType: "invoice.paid",
			want:      false,
		},
		{
			name: "event type match",
			filter: EventFilter{
				EventTypes: []string{"invoice.paid"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      true,
		},
		{
			name: "event type no match",
			filter: EventFilter{
				EventTypes: []string{"invoice.paid"},
			},
			source:    "billing",
			eventType: "invoice.failed",
			want:      false,
		},
		{
			name: "both filters match",
			filter: EventFilter{
				Sources:    []string{"billing"},
				EventTypes: []string{"invoice.paid"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      true,
		},
		{
			name: "source matches but type does not",
			filter: EventFilter{
				Sources:    []string{"billing"},
				EventTypes: []string{"invoice.paid"},
			},
			source:    "billing",
			eventType: "invoice.failed",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.filter.Matches(tt.source, tt.eventType)
			if got != tt.want {
				t.Fatalf(
					"Matches(%q, %q) = %v, want %v",
					tt.source,
					tt.eventType,
					got,
					tt.want,
				)
			}
		})
	}
}
func TestEndpointTenantIDBSONSerialization(t *testing.T) {
	endpoint := Endpoint{
		TenantID: "tenant_123",
		Name:     "billing",
		URL:      "https://example.com/hook",
		Enabled:  true,
	}

	data, err := bson.Marshal(endpoint)
	if err != nil {
		t.Fatalf("bson.Marshal() error = %v", err)
	}

	var decoded bson.M
	if err := bson.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("bson.Unmarshal() error = %v", err)
	}

	got, ok := decoded["tenant_id"]
	if !ok {
		t.Fatal("tenant_id field missing from BSON")
	}

	if got != "tenant_123" {
		t.Fatalf("tenant_id = %v, want %q", got, "tenant_123")
	}
}

func TestEndpointTenantIDExcludedFromJSON(t *testing.T) {
	endpoint := Endpoint{
		TenantID: "tenant_123",
		Name:     "billing",
		URL:      "https://example.com/hook",
		Enabled:  true,
	}

	data, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if string(data) == "" {
		t.Fatal("JSON output is empty")
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if _, ok := decoded["tenant_id"]; ok {
		t.Fatal("tenant_id must not be exposed in JSON")
	}
}
