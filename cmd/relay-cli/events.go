package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	relay "github.com/rajputomsingh/relay/sdk-go"
	"github.com/spf13/cobra"
)

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Send, replay, and list events",
	}
	cmd.AddCommand(newEventsSendCmd(), newEventsReplayCmd(), newEventsListCmd())
	return cmd
}

func newEventsSendCmd() *cobra.Command {
	var (
		source    string
		eventType string
		data      string
		uniqueID  string
	)
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send an event",
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" || eventType == "" {
				return fmt.Errorf("--source and --type are required")
			}
			payload := map[string]any{}
			if data != "" {
				raw, err := readJSONArg(data)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &payload); err != nil {
					return fmt.Errorf("invalid --data JSON: %w", err)
				}
			}
			if uniqueID == "" {
				uniqueID = fmt.Sprintf("cli-%d", time.Now().UnixNano())
			}
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			res, err := client.SendEvent(context.Background(), relay.Event{
				Source:    source,
				EventType: eventType,
				Data:      payload,
			}, uniqueID)
			if err != nil {
				return fmt.Errorf("send event: %w", err)
			}
			switch res.Status {
			case "accepted":
				fmt.Printf("Sent event %s\n", res.EventID)
			case "already_accepted":
				fmt.Printf("Event already accepted (idempotency key %s); original ID not returned\n", uniqueID)
			default:
				fmt.Printf("Status: %s\n", res.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "Event source (required)")
	cmd.Flags().StringVar(&eventType, "type", "", "Event type (required)")
	cmd.Flags().StringVar(&data, "data", "", "Event payload as JSON or @file.json")
	cmd.Flags().StringVar(&uniqueID, "unique-id", "", "Idempotency key (auto-generated if omitted)")
	return cmd
}

func newEventsReplayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "replay <event-id>",
		Short: "Re-fire a previously-sent event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			res, err := client.ReplayEvent(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("replay event: %w", err)
			}
			fmt.Printf("Replay status: %s\n", res.Status)
			fmt.Printf("  event:    %s\n", res.EventID)
			fmt.Printf("  requeued: %d delivery(ies)\n", res.DeliveriesRequeued)
			return nil
		},
	}
}

func newEventsListCmd() *cobra.Command {
	var (
		status string
		limit  int
		cursor string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent events",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			resp, err := client.ListEvents(context.Background(), relay.ListEventsParams{
				DeliveryStatus: status,
				Limit:          limit,
				Cursor:         cursor,
			})
			if err != nil {
				return fmt.Errorf("list events: %w", err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tSOURCE\tTYPE\tSTATUS\tDELIVERY\tRECEIVED"); err != nil {
				return err
			}
			for _, ev := range resp.Events {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					ev.ID, ev.Source, ev.EventType, ev.Status, ev.DeliveryStatus, ev.ReceivedAt); err != nil {
					return err
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Printf("\n%d event(s) shown", len(resp.Events))
			if resp.HasMore && resp.NextCursor != "" {
				fmt.Printf("; more available (use --cursor %s)", resp.NextCursor)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by delivery_status (pending|processing|delivered|failed|disabled)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Max events to return (1-100, default 50)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor from a previous response")
	return cmd
}

// readJSONArg returns raw bytes for either an inline JSON string or @file.json.
func readJSONArg(arg string) ([]byte, error) {
	if strings.HasPrefix(arg, "@") {
		b, err := os.ReadFile(arg[1:])
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", arg[1:], err)
		}
		return b, nil
	}
	return []byte(arg), nil
}
