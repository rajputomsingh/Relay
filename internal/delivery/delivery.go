package delivery

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Delivery statuses.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusDelivered  = "delivered"
	StatusFailed     = "failed"
)

// Delivery represents one (event, endpoint) pair and its lifecycle.
type Delivery struct {
	ID            bson.ObjectID `bson:"_id,omitempty"          json:"id"`
	EventID       bson.ObjectID `bson:"event_id"               json:"event_id"`
	EndpointID    bson.ObjectID `bson:"endpoint_id"            json:"endpoint_id"`
	Status        string        `bson:"status"                 json:"status"`
	Attempts      int           `bson:"attempts"               json:"attempts"`
	MaxAttempts   int           `bson:"max_attempts"           json:"max_attempts"`
	NextAttemptAt *time.Time    `bson:"next_attempt_at,omitempty" json:"next_attempt_at,omitempty"`
	LeaseUntil    *time.Time    `bson:"lease_until,omitempty"  json:"-"`
	LeaseToken    string        `bson:"lease_token,omitempty"  json:"-"`
	DeliveredAt   *time.Time    `bson:"delivered_at,omitempty" json:"delivered_at,omitempty"`
	FailedAt      *time.Time    `bson:"failed_at,omitempty"    json:"failed_at,omitempty"`
	LastError     string        `bson:"last_error,omitempty"   json:"last_error,omitempty"`
	CreatedAt     time.Time     `bson:"created_at"             json:"created_at"`
	UpdatedAt     time.Time     `bson:"updated_at"             json:"updated_at"`
}

// IsTerminal reports whether the delivery has reached a final state.
func (d Delivery) IsTerminal() bool {
	return d.Status == StatusDelivered || d.Status == StatusFailed
}
