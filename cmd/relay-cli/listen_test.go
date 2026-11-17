package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
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

func TestListenFilter(t *testing.T) {
	ev := relay.EventInspection{Source: "my-app", EventType: "user.created"}
	tests := []struct {
		name, sources, types string
		want                 bool
	}{
		{"empty accepts all", "", "", true},
		{"source matches", "my-app", "", true},
		{"source rejects", "other-app", "", false},
		{"type matches", "", "user.created", true},
		{"type rejects", "", "user.deleted", false},
		{"multiple values", "other-app,my-app", "user.updated,user.created", true},
		{"both must match source", "other-app", "user.created", false},
		{"both must match type", "my-app", "user.deleted", false},
		{"whitespace and duplicate values", " my-app, my-app, ", " user.created, ", true},
		{"blank entries accept all", " , , ", " , ", true},
		{"case sensitive", "My-app", "user.created", false},
		{"no substring matching", "my", "user", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newListenFilter(tt.sources, tt.types).matches(ev); got != tt.want {
				t.Errorf("matches() = %v; want %v", got, tt.want)
			}
		})
	}
	if !(listenFilter{}).matches(relay.EventInspection{}) {
		t.Error("zero-value filter should accept events with empty fields")
	}
	if newListenFilter("my-app", "user.created").matches(relay.EventInspection{}) {
		t.Error("nonempty filter should reject empty fields")
	}
}

func TestPollOnceFiltersForwardingAndOutput(t *testing.T) {
	events := []relay.EventInspection{
		{ID: "evt_created", Source: "my-app", EventType: "user.created"},
		{ID: "evt_updated", Source: "other-app", EventType: "user.updated"},
		{ID: "evt_wrong_source", Source: "excluded-app", EventType: "user.created"},
		{ID: "evt_wrong_type", Source: "my-app", EventType: "user.deleted"},
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(relay.ListEventsResponse{Events: events}); err != nil {
			t.Errorf("encode events: %v", err)
		}
	}))
	defer api.Close()
	received := make(chan string, 16)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("X-Relay-Event-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	output, err := os.CreateTemp(t.TempDir(), "listen-output")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = output
	t.Cleanup(func() { os.Stdout = original; _ = output.Close() })
	client := relay.NewClient("test-key", relay.WithBaseURL(api.URL))
	seen := make(map[string]bool)
	filter := newListenFilter("my-app,other-app", "user.created,user.updated")
	for range 2 {
		if err := pollOnce(context.Background(), client, receiver.URL, "", seen, filter); err != nil {
			t.Fatal(err)
		}
	}
	os.Stdout = original
	var got []string
	for len(received) > 0 {
		got = append(got, <-received)
	}
	if want := []string{"evt_created", "evt_updated"}; !reflect.DeepEqual(got, want) {
		t.Errorf("forwarded = %v; want %v (once each)", got, want)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"evt_created", "evt_updated"} {
		if strings.Count(string(content), id) != 1 {
			t.Errorf("expected one output line for %s: %s", id, content)
		}
	}
	for _, id := range []string{"evt_wrong_source", "evt_wrong_type"} {
		if strings.Contains(string(content), id) {
			t.Errorf("filtered event was printed: %s", content)
		}
	}
	// Omitting filters preserves the existing forwarding behavior.
	if err := pollOnce(context.Background(), client, receiver.URL, "", make(map[string]bool), listenFilter{}); err != nil {
		t.Fatal(err)
	}
	if len(received) != len(events) {
		t.Errorf("unfiltered forwarded %d events; want %d", len(received), len(events))
	}
}

func TestListenFilterFlags(t *testing.T) {
	cmd := newListenCmd()
	if err := cmd.ParseFlags([]string{"--filter-source", "my-app,other-app", "--filter-type", "user.created,user.updated"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"filter-source": "my-app,other-app", "filter-type": "user.created,user.updated"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || flag.Value.String() != want || flag.DefValue != "" {
			t.Errorf("unexpected %s flag: %v", name, flag)
		}
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Help(); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--filter-source", "--filter-type"} {
		if !strings.Contains(out.String(), flag) {
			t.Errorf("help missing %s: %s", flag, out.String())
		}
	}
}
