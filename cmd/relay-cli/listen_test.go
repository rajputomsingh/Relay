package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	relay "github.com/rajputomsingh/relay/sdk-go"
)

func TestForwardPostsWithSignature(t *testing.T) {
	var (
		posts   int32
		gotEID  atomic.Value
		gotTS   atomic.Value
		gotSig  atomic.Value
		gotBody atomic.Value
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody.Store(string(buf))
		gotEID.Store(r.Header.Get("X-Relay-Event-ID"))
		gotTS.Store(r.Header.Get("X-Relay-Timestamp"))
		gotSig.Store(r.Header.Get("X-Relay-Signature"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ev := relay.EventInspection{ID: "evt_test123", Source: "test", EventType: "test.ping"}
	secret := "test-signing-secret-not-real-1234567890"
	if err := forward(context.Background(), srv.URL, secret, ev); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("posts = %d; want 1", posts)
	}
	if gotEID.Load().(string) != ev.ID {
		t.Errorf("X-Relay-Event-ID = %q; want %q", gotEID.Load(), ev.ID)
	}
	if gotTS.Load().(string) == "" {
		t.Error("X-Relay-Timestamp missing")
	}
	if gotSig.Load().(string) == "" {
		t.Error("X-Relay-Signature missing")
	}
	body := gotBody.Load().(string)
	if len(body) == 0 || body[0] != '{' {
		t.Errorf("body not JSON: %q", body)
	}
}

func TestForwardReportsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	ev := relay.EventInspection{ID: "evt_x", Source: "s", EventType: "t"}
	if err := forward(context.Background(), srv.URL, "", ev); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}
