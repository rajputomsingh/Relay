package delivery

import (
	"testing"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestDeliveryUniqueKeyShape verifies that the delivery document carries the
// (event_id, endpoint_id) pair that the unique index relies on.
func TestDeliveryUniqueKeyShape(t *testing.T) {
	eventID := bson.NewObjectID()
	endpointID := bson.NewObjectID()

	d := Delivery{
		EventID:     eventID,
		EndpointID:  endpointID,
		Status:      StatusPending,
		MaxAttempts: 8,
	}

	if d.EventID != eventID {
		t.Fatalf("event id mismatch")
	}
	if d.EndpointID != endpointID {
		t.Fatalf("endpoint id mismatch")
	}
	if d.IsTerminal() {
		t.Fatal("pending delivery should not be terminal")
	}

	d.Status = StatusDelivered
	if !d.IsTerminal() {
		t.Fatal("delivered delivery should be terminal")
	}

	d.Status = StatusFailed
	if !d.IsTerminal() {
		t.Fatal("failed delivery should be terminal")
	}
}

// TestEventFilterRespectedByFanoutLogic mirrors the filter decision
// that Fanout.CreateDeliveries applies while iterating endpoints.
func TestEventFilterRespectedByFanoutLogic(t *testing.T) {
	cases := []struct {
		name      string
		filter    endpoints.EventFilter
		source    string
		eventType string
		want      bool
	}{
		{
			name:      "no filter matches all",
			filter:    endpoints.EventFilter{},
			source:    "billing",
			eventType: "invoice.paid",
			want:      true,
		},
		{
			name: "source filter excludes",
			filter: endpoints.EventFilter{
				Sources: []string{"orders"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      false,
		},
		{
			name: "type filter excludes",
			filter: endpoints.EventFilter{
				EventTypes: []string{"order.created"},
			},
			source:    "billing",
			eventType: "invoice.paid",
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.filter.Matches(tc.source, tc.eventType)
			if got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}
