package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const endpointsTimeout = 5 * time.Second

type endpointsStore interface {
	Create(context.Context, endpoints.Endpoint) (endpoints.Endpoint, error)
	List(context.Context) ([]endpoints.Endpoint, error)
	Get(context.Context, bson.ObjectID) (endpoints.Endpoint, error)
	Update(context.Context, bson.ObjectID, endpoints.Update) (endpoints.Endpoint, error)
	Disable(context.Context, bson.ObjectID) (endpoints.Endpoint, error)
}

// EndpointsHandler exposes endpoint CRUD over HTTP.
type EndpointsHandler struct {
	store  endpointsStore
	apiKey string
}

// NewEndpointsHandler creates an EndpointsHandler.
func NewEndpointsHandler(store endpointsStore, apiKey string) *EndpointsHandler {
	return &EndpointsHandler{store: store, apiKey: apiKey}
}

// Register wires the handler into the mux.
func (h *EndpointsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/endpoints", h.create)
	mux.HandleFunc("GET /v1/endpoints", h.list)
	mux.HandleFunc("GET /v1/endpoints/{id}", h.get)
	mux.HandleFunc("PATCH /v1/endpoints/{id}", h.update)
	mux.HandleFunc("DELETE /v1/endpoints/{id}", h.delete)
}

// endpointResponse is the safe projection returned to clients.
// It never contains the signing secret.
type endpointResponse struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	URL              string                `json:"url"`
	HasSigningSecret bool                  `json:"has_signing_secret"`
	Enabled          bool                  `json:"enabled"`
	EventFilter      endpoints.EventFilter `json:"event_filter,omitempty"`
	MaxAttempts      int                   `json:"max_attempts"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	DisabledAt       *time.Time            `json:"disabled_at,omitempty"`
}

type endpointListResponse struct {
	Endpoints []endpointResponse `json:"endpoints"`
	Count     int                `json:"count"`
}

type createEndpointRequest struct {
	Name          string                `json:"name"`
	URL           string                `json:"url"`
	SigningSecret string                `json:"signing_secret"`
	Enabled       *bool                 `json:"enabled"`
	EventFilter   endpoints.EventFilter `json:"event_filter"`
	MaxAttempts   int                   `json:"max_attempts"`
}

type updateEndpointRequest struct {
	Name          *string                `json:"name"`
	URL           *string                `json:"url"`
	SigningSecret *string                `json:"signing_secret"`
	Enabled       *bool                  `json:"enabled"`
	EventFilter   *endpoints.EventFilter `json:"event_filter"`
	MaxAttempts   *int                   `json:"max_attempts"`
}

// ---------------------------------------------
// HTTP methods
// ---------------------------------------------

func (h *EndpointsHandler) create(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	var req createEndpointRequest
	if !decodeBody(w, r, &req) {
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	ep := endpoints.Endpoint{
		Name:          req.Name,
		URL:           req.URL,
		SigningSecret: req.SigningSecret,
		Enabled:       enabled,
		EventFilter:   req.EventFilter,
		MaxAttempts:   req.MaxAttempts,
	}

	if err := ep.Validate(); err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), endpointsTimeout)
	defer cancel()

	created, err := h.store.Create(ctx, ep)
	if err != nil {
		writeEndpointStoreError(w, err, "create")
		return
	}

	writeEventJSON(w, http.StatusCreated, toEndpointResponse(created))
}

func (h *EndpointsHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), endpointsTimeout)
	defer cancel()

	eps, err := h.store.List(ctx)
	if err != nil {
		writeEndpointStoreError(w, err, "list")
		return
	}

	resp := endpointListResponse{
		Endpoints: make([]endpointResponse, 0, len(eps)),
		Count:     len(eps),
	}
	for _, ep := range eps {
		resp.Endpoints = append(resp.Endpoints, toEndpointResponse(ep))
	}

	writeEventJSON(w, http.StatusOK, resp)
}

func (h *EndpointsHandler) get(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	id, ok := parseEndpointID(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), endpointsTimeout)
	defer cancel()

	ep, err := h.store.Get(ctx, id)
	if err != nil {
		writeEndpointStoreError(w, err, "get")
		return
	}

	writeEventJSON(w, http.StatusOK, toEndpointResponse(ep))
}

func (h *EndpointsHandler) update(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	id, ok := parseEndpointID(w, r)
	if !ok {
		return
	}

	var req updateEndpointRequest
	if !decodeBody(w, r, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), endpointsTimeout)
	defer cancel()

	updated, err := h.store.Update(ctx, id, endpoints.Update{
		Name:          req.Name,
		URL:           req.URL,
		SigningSecret: req.SigningSecret,
		EventFilter:   req.EventFilter,
		MaxAttempts:   req.MaxAttempts,
		Enabled:       req.Enabled,
	})
	if err != nil {
		writeEndpointStoreError(w, err, "update")
		return
	}

	writeEventJSON(w, http.StatusOK, toEndpointResponse(updated))
}

func (h *EndpointsHandler) delete(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	id, ok := parseEndpointID(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), endpointsTimeout)
	defer cancel()

	if _, err := h.store.Disable(ctx, id); err != nil {
		writeEndpointStoreError(w, err, "disable")
		return
	}

	writeEventJSON(w, http.StatusOK, map[string]any{
		"status": "disabled",
		"id":     id.Hex(),
	})
}

// ---------------------------------------------
// Helpers
// ---------------------------------------------

func (h *EndpointsHandler) authorized(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")

	if !strings.HasPrefix(header, prefix) ||
		h.apiKey == "" ||
		subtle.ConstantTimeCompare(
			[]byte(strings.TrimPrefix(header, prefix)),
			[]byte(h.apiKey),
		) != 1 {
		writeEventJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "unauthorized",
		})
		return false
	}

	return true
}

func parseEndpointID(w http.ResponseWriter, r *http.Request) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid endpoint ID",
		})
		return bson.ObjectID{}, false
	}
	return id, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()

	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		writeEventJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid request body",
		})
		return false
	}

	return true
}

func toEndpointResponse(ep endpoints.Endpoint) endpointResponse {
	return endpointResponse{
		ID:               ep.ID.Hex(),
		Name:             ep.Name,
		URL:              ep.URL,
		HasSigningSecret: ep.SigningSecret != "",
		Enabled:          ep.Enabled,
		EventFilter:      ep.EventFilter,
		MaxAttempts:      ep.MaxAttempts,
		CreatedAt:        ep.CreatedAt,
		UpdatedAt:        ep.UpdatedAt,
		DisabledAt:       ep.DisabledAt,
	}
}

func writeEndpointStoreError(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, endpoints.ErrNotFound):
		writeEventJSON(w, http.StatusNotFound, map[string]any{
			"error": "endpoint not found",
		})
	case errors.Is(err, endpoints.ErrNameConflict):
		writeEventJSON(w, http.StatusConflict, map[string]any{
			"error": "endpoint name already exists",
		})
	default:
		// Do not leak internal errors.
		writeEventJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "endpoint " + op + " temporarily unavailable",
		})
	}
}
