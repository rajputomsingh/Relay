package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// APIError represents an error returned by the Relay API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf(
		"relay api error: status=%d message=%s",
		e.StatusCode, e.Message,
	)
}

// IsNotFound reports whether err is a Relay API error with HTTP 404.
// Use with errors.Is.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound
}

// IsConflict reports whether err is a Relay API error with HTTP 409.
// Use with errors.Is.
//
// Typical causes: replay requested for an event that is not in `failed`
// state; endpoint name already exists.
func IsConflict(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusConflict
}

// IsUnauthorized reports whether err is a Relay API error with HTTP 401.
// Use with errors.Is.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusUnauthorized
}

// IsRateLimited reports whether err is a Relay API error with HTTP 429.
// Use with errors.Is.
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusTooManyRequests
}

func parseError(resp *http.Response) error {
	var errResp struct {
		Error string `json:"error"`
	}

	// Ignore decode errors; we just want the best-effort message.
	_ = json.NewDecoder(resp.Body).Decode(&errResp)

	msg := errResp.Error
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}

	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    msg,
	}
}
