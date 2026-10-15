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
	ID            bson.ObjectID `bson:"_id,omitempty"             json:"id"`
	Name          string        `bson:"name"                      json:"name"`
	URL           string        `bson:"url"                       json:"url"`
	SigningSecret string        `bson:"signing_secret,omitempty"  json:"-"`
	Enabled       bool          `bson:"enabled"                   json:"enabled"`
	EventFilter   EventFilter   `bson:"event_filter,omitempty"    json:"event_filter,omitempty"`
	MaxAttempts   int           `bson:"max_attempts"              json:"max_attempts"`
	RateLimitRPS  int           `bson:"rate_limit_rps,omitempty"  json:"rate_limit_rps,omitempty"`
	CreatedAt     time.Time     `bson:"created_at"                json:"created_at"`
	UpdatedAt     time.Time     `bson:"updated_at"                json:"updated_at"`
	DisabledAt    *time.Time    `bson:"disabled_at,omitempty"     json:"disabled_at,omitempty"`
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
	case e.RateLimitRPS < 0 || e.RateLimitRPS > 1000:
		return errors.New(
			"rate_limit_rps must be 0 (unlimited) or between 1 and 1000",
		)
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

// Update describes a partial update to an endpoint.
// Nil fields are left unchanged.
type Update struct {
	Name          *string      `json:"name,omitempty"`
	URL           *string      `json:"url,omitempty"`
	SigningSecret *string      `json:"signing_secret,omitempty"`
	EventFilter   *EventFilter `json:"event_filter,omitempty"`
	MaxAttempts   *int         `json:"max_attempts,omitempty"`
	RateLimitRPS  *int         `json:"rate_limit_rps,omitempty"`
	Enabled       *bool        `json:"enabled,omitempty"`
}

// Apply merges the update into the endpoint and validates the result.
func (u Update) Apply(e *Endpoint) error {
	if u.Name != nil {
		e.Name = *u.Name
	}
	if u.URL != nil {
		e.URL = *u.URL
	}
	if u.SigningSecret != nil {
		e.SigningSecret = *u.SigningSecret
	}
	if u.EventFilter != nil {
		e.EventFilter = *u.EventFilter
	}
	if u.MaxAttempts != nil {
		e.MaxAttempts = *u.MaxAttempts
	}
	if u.RateLimitRPS != nil {
		e.RateLimitRPS = *u.RateLimitRPS
	}
	if u.Enabled != nil {
		e.Enabled = *u.Enabled
		if !e.Enabled && e.DisabledAt == nil {
			now := time.Now().UTC()
			e.DisabledAt = &now
		}
		if e.Enabled {
			e.DisabledAt = nil
		}
	}
	return e.Validate()
}
