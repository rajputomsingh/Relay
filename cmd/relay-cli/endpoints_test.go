package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setHealthAPI(t *testing.T, url string) {
	t.Helper()
	oldURL, oldKey := apiURL, apiKey
	apiURL, apiKey = url, "test-key"
	t.Cleanup(func() { apiURL, apiKey = oldURL, oldKey })
}

func TestEndpointsHealthOutput(t *testing.T) {
	for _, tc := range []struct{ name, timestamps, want string }{
		{"timestamps", `"last_success_at":"2026-11-17T09:30:00.123Z","last_failure_at":"2026-11-17T09:00:00+09:00"`, "last success:          2026-11-17T09:30:00.123Z\nlast failure:          2026-11-17T09:00:00+09:00\n"},
		{"nil timestamps", `"last_success_at":null,"last_failure_at":null`, "last success:          -\nlast failure:          -\n"},
		{"one nil timestamp", `"last_success_at":"2026-11-17T09:30:00Z","last_failure_at":null`, "last success:          2026-11-17T09:30:00Z\nlast failure:          -\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/endpoints/ep_test/health" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
					t.Errorf("Authorization = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"success_rate_1h":95,"attempts_1h":100,"successes_1h":95,"failures_1h":5,"consecutive_failures":2,%s}`, tc.timestamps)
			}))
			defer srv.Close()
			setHealthAPI(t, srv.URL)
			var out bytes.Buffer
			cmd := newEndpointsCmd()
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"health", "ep_test"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			want := "success rate (1h):     95%\nattempts (1h):         100\nsuccesses (1h):        95\nfailures (1h):         5\nconsecutive failures:  2\n" + tc.want
			if out.String() != want {
				t.Errorf("output = %q; want %q", out.String(), want)
			}
		})
	}
}

func TestEndpointsHealthArgsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"health"}, {"health", "one", "two"}} {
		cmd := newEndpointsCmd()
		cmd.SetArgs(args)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.Execute(); err == nil {
			t.Errorf("args %v: expected error", args)
		}
	}
	var out bytes.Buffer
	cmd := newEndpointsCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"health", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"health <id>", "past hour", "dash"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help missing %q: %s", want, out.String())
		}
	}
}

func TestEndpointsHealthErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"endpoint not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	setHealthAPI(t, srv.URL)
	var out bytes.Buffer
	cmd := newEndpointsHealthCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"missing"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "404") || !strings.HasPrefix(err.Error(), "get endpoint health: ") {
		t.Fatalf("expected API 404 error, got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("unexpected health output: %s", out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd = newEndpointsHealthCmd()
	cmd.SetArgs([]string{"ep_test"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestEndpointsHealthPercentages(t *testing.T) {
	for _, rate := range []string{"0", "95.5", "100"} {
		t.Run(rate, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"success_rate_1h":%s}`, rate)
			}))
			defer srv.Close()
			setHealthAPI(t, srv.URL)
			var out bytes.Buffer
			cmd := newEndpointsHealthCmd()
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"ep_test"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if want := "success rate (1h):     " + rate + "%\n"; !strings.HasPrefix(out.String(), want) {
				t.Errorf("output = %q; want prefix %q", out.String(), want)
			}
		})
	}
}

type healthErrorWriter struct{ err error }

func (w healthErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestEndpointsHealthOutputError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	setHealthAPI(t, srv.URL)
	want := errors.New("output unavailable")
	cmd := newEndpointsHealthCmd()
	cmd.SetOut(healthErrorWriter{err: want})
	cmd.SetArgs([]string{"ep_test"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); !errors.Is(err, want) {
		t.Fatalf("expected output error, got %v", err)
	}
}
