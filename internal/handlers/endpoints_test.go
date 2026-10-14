package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajputomsingh/relay/internal/endpoints"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeEndpointsStore struct {
	created   endpoints.Endpoint
	listItems []endpoints.Endpoint
	getItem   endpoints.Endpoint
	updated   endpoints.Endpoint
	disabled  endpoints.Endpoint

	err error
}

func (f *fakeEndpointsStore) Create(_ context.Context, ep endpoints.Endpoint) (endpoints.Endpoint, error) {
	if f.err != nil {
		return endpoints.Endpoint{}, f.err
	}
	ep.ID = bson.NewObjectID()
	f.created = ep
	return ep, nil
}

func (f *fakeEndpointsStore) List(_ context.Context) ([]endpoints.Endpoint, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.listItems, nil
}

func (f *fakeEndpointsStore) Get(_ context.Context, id bson.ObjectID) (endpoints.Endpoint, error) {
	if f.err != nil {
		return endpoints.Endpoint{}, f.err
	}
	if f.getItem.ID != id {
		return endpoints.Endpoint{}, endpoints.ErrNotFound
	}
	return f.getItem, nil
}

func (f *fakeEndpointsStore) Update(_ context.Context, id bson.ObjectID, u endpoints.Update) (endpoints.Endpoint, error) {
	if f.err != nil {
		return endpoints.Endpoint{}, f.err
	}
	ep := f.updated
	if ep.ID == bson.NilObjectID {
		ep.ID = id
	}
	return ep, nil
}

func (f *fakeEndpointsStore) Disable(_ context.Context, id bson.ObjectID) (endpoints.Endpoint, error) {
	if f.err != nil {
		return endpoints.Endpoint{}, f.err
	}
	ep := f.disabled
	if ep.ID == bson.NilObjectID {
		ep.ID = id
	}
	return ep, nil
}

func newEndpointsTestMux(store endpointsStore) *http.ServeMux {
	mux := http.NewServeMux()
	NewEndpointsHandler(store, testAPIKey).Register(mux)
	return mux
}

func performEndpointRequest(
	mux http.Handler,
	method, path, body, apiKey string,
) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequest(method, path, reader)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestEndpointsUnauthorized(t *testing.T) {
	mux := newEndpointsTestMux(&fakeEndpointsStore{})

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/v1/endpoints", `{}`},
		{http.MethodGet, "/v1/endpoints", ""},
		{http.MethodGet, "/v1/endpoints/000000000000000000000000", ""},
		{http.MethodPatch, "/v1/endpoints/000000000000000000000000", `{}`},
		{http.MethodDelete, "/v1/endpoints/000000000000000000000000", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := performEndpointRequest(mux, tc.method, tc.path, tc.body, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEndpointsMethodNotAllowed(t *testing.T) {
	mux := newEndpointsTestMux(&fakeEndpointsStore{})

	rec := performEndpointRequest(
		mux, http.MethodPut, "/v1/endpoints", `{}`, testAPIKey,
	)
	if rec.Code != http.StatusMethodNotAllowed &&
		rec.Code != http.StatusNotFound {
		t.Fatalf("expected 405 or 404, got %d", rec.Code)
	}
}

func TestEndpointsCreateValidation(t *testing.T) {
	mux := newEndpointsTestMux(&fakeEndpointsStore{})

	// Missing URL.
	body := `{"name":"billing","max_attempts":8}`
	rec := performEndpointRequest(mux, http.MethodPost, "/v1/endpoints", body, testAPIKey)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing URL, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown field.
	body = `{"name":"billing","url":"https://example.com/hook","max_attempts":8,"unexpected":true}`
	rec = performEndpointRequest(mux, http.MethodPost, "/v1/endpoints", body, testAPIKey)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointsCreateSuccessDoesNotLeakSecret(t *testing.T) {
	store := &fakeEndpointsStore{}
	mux := newEndpointsTestMux(store)

	body := `{
        "name":"billing",
        "url":"https://example.com/hook",
        "signing_secret":"0123456789abcdef0123456789abcdef",
        "max_attempts":8
    }`

	rec := performEndpointRequest(mux, http.MethodPost, "/v1/endpoints", body, testAPIKey)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), "0123456789abcdef") {
		t.Fatal("response leaked the signing secret")
	}

	var resp endpointResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.HasSigningSecret {
		t.Fatal("expected has_signing_secret true")
	}
	if resp.Name != "billing" {
		t.Fatalf("unexpected name: %q", resp.Name)
	}
}

func TestEndpointsGetNotFound(t *testing.T) {
	store := &fakeEndpointsStore{
		getItem: endpoints.Endpoint{
			ID: bson.NewObjectID(),
		},
	}
	mux := newEndpointsTestMux(store)

	otherID := bson.NewObjectID()
	rec := performEndpointRequest(
		mux, http.MethodGet, "/v1/endpoints/"+otherID.Hex(), "", testAPIKey,
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestEndpointsListShape(t *testing.T) {
	store := &fakeEndpointsStore{
		listItems: []endpoints.Endpoint{
			{
				ID:          bson.NewObjectID(),
				Name:        "billing",
				URL:         "https://billing.example.com/hook",
				Enabled:     true,
				MaxAttempts: 8,
			},
			{
				ID:          bson.NewObjectID(),
				Name:        "orders",
				URL:         "https://orders.example.com/hook",
				Enabled:     true,
				MaxAttempts: 5,
			},
		},
	}
	mux := newEndpointsTestMux(store)

	rec := performEndpointRequest(mux, http.MethodGet, "/v1/endpoints", "", testAPIKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp endpointListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("expected count 2, got %d", resp.Count)
	}
	if len(resp.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(resp.Endpoints))
	}
}

func TestEndpointsUpdate(t *testing.T) {
	id := bson.NewObjectID()
	store := &fakeEndpointsStore{
		updated: endpoints.Endpoint{
			ID:          id,
			Name:        "billing-v2",
			URL:         "https://new.example.com/hook",
			Enabled:     true,
			MaxAttempts: 5,
		},
	}
	mux := newEndpointsTestMux(store)

	body := `{"name":"billing-v2","url":"https://new.example.com/hook","max_attempts":5}`
	rec := performEndpointRequest(
		mux, http.MethodPatch, "/v1/endpoints/"+id.Hex(), body, testAPIKey,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointsDelete(t *testing.T) {
	id := bson.NewObjectID()
	store := &fakeEndpointsStore{
		disabled: endpoints.Endpoint{
			ID:      id,
			Name:    "billing",
			URL:     "https://example.com/hook",
			Enabled: false,
		},
	}
	mux := newEndpointsTestMux(store)

	rec := performEndpointRequest(
		mux, http.MethodDelete, "/v1/endpoints/"+id.Hex(), "", testAPIKey,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if resp["status"] != "disabled" {
		t.Fatalf("expected status disabled, got %v", resp["status"])
	}
}

func TestEndpointsCreateConflict(t *testing.T) {
	store := &fakeEndpointsStore{err: endpoints.ErrNameConflict}
	mux := newEndpointsTestMux(store)

	body := `{"name":"billing","url":"https://example.com/hook","max_attempts":8}`
	rec := performEndpointRequest(mux, http.MethodPost, "/v1/endpoints", body, testAPIKey)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEndpointsStoreFailureDoesNotLeak(t *testing.T) {
	store := &fakeEndpointsStore{err: errors.New("mongodb internal: secret URI")}
	mux := newEndpointsTestMux(store)

	rec := performEndpointRequest(mux, http.MethodGet, "/v1/endpoints", "", testAPIKey)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "mongodb internal") {
		t.Fatal("response leaked internal error details")
	}
}
