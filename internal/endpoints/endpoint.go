// Package endpoints defines webhook destinations and their lifecycle.
package endpoints

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// EventFilter selects which events an endpoint receives.
// An empty slice means "match all".
type EventFilter struct {
	EventTypes []string `bson:"event_types,omitempty" json:"event_types,omitempty"`
	Sources    []string `bson:"sources,omitempty"     json:"sources,omitempty"`
}

// Matches reports whether the filter accepts the given event.
// Empty filters match everything.
func (f EventFilter) Matches(source, eventType string) bool {
	if len(f.Sources) > 0 {
		found := false
		for _, s := range f.Sources {
			if s == source {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(f.EventTypes) > 0 {
		found := false
		for _, t := range f.EventTypes {
			if t == eventType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// Endpoint is a webhook destination.
type Endpoint struct {
	ID            bson.ObjectID `bson:"_id,omitempty"          json:"id"`
	Name          string        `bson:"name"                   json:"name"`
	URL           string        `bson:"url"                    json:"url"`
	SigningSecret string        `bson:"signing_secret,omitempty" json:"-"`
	Enabled       bool          `bson:"enabled"                json:"enabled"`
	EventFilter   EventFilter   `bson:"event_filter,omitempty" json:"event_filter,omitempty"`
	MaxAttempts   int           `bson:"max_attempts"           json:"max_attempts"`
	CreatedAt     time.Time     `bson:"created_at"             json:"created_at"`
	UpdatedAt     time.Time     `bson:"updated_at"             json:"updated_at"`
	DisabledAt    *time.Time    `bson:"disabled_at,omitempty"  json:"disabled_at,omitempty"`
}

// Validate checks the endpoint's fields before persisting.
// It does not check uniqueness; that is enforced by the database.
func (e *Endpoint) Validate() error {
	e.Name = strings.TrimSpace(e.Name)
	e.URL = strings.TrimSpace(e.URL)
	e.SigningSecret = strings.TrimSpace(e.SigningSecret)

	switch {
	case e.Name == "":
		return errors.New("endpoint name is required")
	case len(e.Name) > 200:
		return errors.New("endpoint name must be at most 200 characters")
	case e.URL == "":
		return errors.New("endpoint URL is required")
	case e.MaxAttempts < 1 || e.MaxAttempts > 20:
		return errors.New("max_attempts must be between 1 and 20")
	}

	u, err := url.ParseRequestURI(e.URL)
	if err != nil || u.Hostname() == "" ||
		(u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil {
		return errors.New(
			"endpoint URL must be an absolute HTTP(S) URL without user information",
		)
	}

	if e.SigningSecret != "" && len(e.SigningSecret) < 32 {
		return errors.New(
			"signing secret must contain at least 32 characters",
		)
	}

	return nil
}
