package delivery

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Attempt records one webhook delivery attempt for one endpoint.
type Attempt struct {
	ID            bson.ObjectID `bson:"_id,omitempty" json:"id"`
	EventID       bson.ObjectID `bson:"event_id" json:"event_id"`
	EndpointID    bson.ObjectID `bson:"endpoint_id,omitempty" json:"endpoint_id,omitempty"`
	AttemptNumber int           `bson:"attempt_number" json:"attempt_number"`
	StartedAt     time.Time     `bson:"started_at" json:"started_at"`
	FinishedAt    time.Time     `bson:"finished_at" json:"finished_at"`
	DurationMS    int64         `bson:"duration_ms" json:"duration_ms"`
	HTTPStatus    int           `bson:"http_status,omitempty" json:"http_status,omitempty"`
	Outcome       string        `bson:"outcome" json:"outcome"`
	Error         string        `bson:"error,omitempty" json:"error,omitempty"`
}
