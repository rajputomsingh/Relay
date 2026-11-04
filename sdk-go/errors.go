package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIError represents an error returned by the Relay API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("relay api error: status=%d message=%s", e.StatusCode, e.Message)
}

func parseError(resp *http.Response) error {
	var errResp struct {
		Error string `json:"error"`
	}

	// Ignore decode errors, we just want the best-effort message
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
