package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEndpointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "endpoints",
		Short: "Manage Relay endpoints",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "create",
			Short: "Create an endpoint",
			RunE:  func(cmd *cobra.Command, args []string) error { return fmt.Errorf("endpoints create: not implemented") },
		},
		&cobra.Command{
			Use:   "list",
			Short: "List endpoints",
			RunE:  func(cmd *cobra.Command, args []string) error { return fmt.Errorf("endpoints list: not implemented") },
		},
	)
	return cmd
}
