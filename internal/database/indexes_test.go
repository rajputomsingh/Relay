package database

import "testing"

func TestRequiredIndexesContainsTenantIndexes(t *testing.T) {
	tests := map[string][]string{
		"endpoints": {
			"idx_endpoint_tenant_name",
			"idx_endpoint_tenant_enabled",
		},
		"events": {
			"idx_event_tenant_received",
		},
		"deliveries": {
			"idx_delivery_tenant_endpoint_status",
			"idx_delivery_tenant_event",
		},
	}

	for collection, expectedIndexes := range tests {
		t.Run(collection, func(t *testing.T) {
			required, ok := RequiredIndexes[collection]
			if !ok {
				t.Fatalf("RequiredIndexes missing collection %q", collection)
			}

			requiredSet := make(map[string]struct{}, len(required))
			for _, name := range required {
				requiredSet[name] = struct{}{}
			}

			for _, name := range expectedIndexes {
				if _, exists := requiredSet[name]; !exists {
					t.Errorf("RequiredIndexes[%q] missing tenant index %q", collection, name)
				}
			}
		})
	}
}

func TestRequiredIndexesPreservesCriticalDeliveryIndexes(t *testing.T) {
	expected := []string{
		"uniq_delivery_event_endpoint",
		"idx_delivery_due",
		"idx_delivery_lease",
		"idx_delivery_endpoint_status",
		"idx_delivery_event",
	}

	required := RequiredIndexes["deliveries"]

	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[name] = struct{}{}
	}

	for _, name := range expected {
		if _, exists := requiredSet[name]; !exists {
			t.Errorf("RequiredIndexes[deliveries] missing critical index %q", name)
		}
	}
}
