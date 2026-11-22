package delivery

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDeliveryTenantIDBSONSerialization(t *testing.T) {
	delivery := Delivery{
		TenantID: "tenant_123",
		Status:   StatusPending,
	}

	data, err := bson.Marshal(delivery)
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

func TestDeliveryTenantIDExcludedFromJSON(t *testing.T) {
	delivery := Delivery{
		TenantID: "tenant_123",
		Status:   StatusPending,
	}

	data, err := json.Marshal(delivery)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if _, ok := decoded["tenant_id"]; ok {
		t.Fatal("tenant_id must not be exposed in JSON")
	}
}
