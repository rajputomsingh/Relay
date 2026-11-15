package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newListenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Forward Relay events to a local URL (like stripe listen)",
		RunE:  func(cmd *cobra.Command, args []string) error { return fmt.Errorf("listen: not implemented") },
	}
	return cmd
}