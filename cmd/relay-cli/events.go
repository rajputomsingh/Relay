package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Send events to Relay",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "send",
		Short: "Send an event",
		RunE:  func(cmd *cobra.Command, args []string) error { return fmt.Errorf("events send: not implemented") },
	})
	return cmd
}
