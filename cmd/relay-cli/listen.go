package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	relay "github.com/rajputomsingh/relay/sdk-go"
	"github.com/spf13/cobra"
)

func newListenCmd() *cobra.Command {
	var (
		forwardTo    string
		interval     time.Duration
		secret       string
		filterSource string
		filterType   string
	)
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Forward Relay events to a local URL (like stripe listen)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if forwardTo == "" {
				return fmt.Errorf("--forward-to is required (e.g. http://localhost:3000/webhook)")
			}
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))

			if secret != "" {
				fmt.Printf("Signing secret: %s\n", secret)
			}
			fmt.Printf("Forwarding to: %s\n", forwardTo)
			fmt.Printf("Polling every %s\n\n", interval)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			seen := map[string]bool{}
			filter := newListenFilter(filterSource, filterType)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					fmt.Println("\nStopped.")
					return nil
				case <-ticker.C:
					if err := pollOnce(ctx, client, forwardTo, secret, seen, filter); err != nil {
						fmt.Fprintf(os.Stderr, "poll: %v\n", err)
					}
				}
			}
		},
	}
	cmd.Flags().StringVar(&forwardTo, "forward-to", "", "Local URL to forward events to (required)")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Poll interval")
	cmd.Flags().StringVar(&secret, "secret", "", "Signing secret to display and sign forwards with")
	cmd.Flags().StringVar(&filterSource, "filter-source", "", "Comma-separated source values to forward (empty = all)")
	cmd.Flags().StringVar(&filterType, "filter-type", "", "Comma-separated event types to forward (empty = all)")
	return cmd
}

type listenFilter struct {
	sources map[string]bool
	types   map[string]bool
}

func newListenFilter(sources, types string) listenFilter {
	parse := func(value string) map[string]bool {
		values := make(map[string]bool)
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				values[item] = true
			}
		}
		return values
	}
	return listenFilter{sources: parse(sources), types: parse(types)}
}

func (f listenFilter) matches(ev relay.EventInspection) bool {
	return (len(f.sources) == 0 || f.sources[ev.Source]) &&
		(len(f.types) == 0 || f.types[ev.EventType])
}

func pollOnce(ctx context.Context, client *relay.Client, forwardTo, secret string, seen map[string]bool, filter listenFilter) error {
	resp, err := client.ListEvents(ctx, relay.ListEventsParams{})
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}
	for _, ev := range resp.Events {
		if seen[ev.ID] {
			continue
		}
		seen[ev.ID] = true
		if !filter.matches(ev) {
			continue
		}
		if err := forward(ctx, forwardTo, secret, ev); err != nil {
			fmt.Fprintf(os.Stderr, "forward %s: %v\n", ev.ID, err)
		}
	}
	return nil
}

func forward(ctx context.Context, target, secret string, ev relay.EventInspection) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay-Event-ID", ev.ID)
	ts := time.Now().Unix()
	req.Header.Set("X-Relay-Timestamp", fmt.Sprintf("%d", ts))
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(fmt.Appendf(nil, "%d.", ts))
		mac.Write(body)
		req.Header.Set("X-Relay-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("[%s] %s  → ERROR %v\n", time.Now().Format("15:04:05"), ev.ID, err)
		return err
	}
	defer func() {
		// Best-effort cleanup; the forward outcome is determined by the response status.
		_ = resp.Body.Close()
	}()
	_, _ = io.Copy(io.Discard, resp.Body)
	fmt.Printf("[%s] %s  %s/%s  → %d (%s)\n",
		time.Now().Format("15:04:05"),
		ev.ID,
		ev.Source, ev.EventType,
		resp.StatusCode,
		time.Since(start).Round(time.Millisecond),
	)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("forward returned %d", resp.StatusCode)
	}
	return nil
}
