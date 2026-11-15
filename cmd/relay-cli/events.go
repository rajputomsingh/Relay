package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	relay "github.com/rajputomsingh/relay/sdk-go"
	"github.com/spf13/cobra"
)

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Send events to Relay",
	}
	cmd.AddCommand(newEventsSendCmd())
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
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					return fmt.Errorf("invalid --data JSON: %w", err)
				}
			}
			if uniqueID == "" {
				// SendEvent requires an idempotency key; generate one if the user didn't supply it.
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
	cmd.Flags().StringVar(&data, "data", "", "Event payload as JSON")
	cmd.Flags().StringVar(&uniqueID, "unique-id", "", "Idempotency key (auto-generated if omitted)")
	return cmd
}
