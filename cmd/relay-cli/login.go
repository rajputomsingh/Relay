package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save a Relay API key to ~/.relay/config.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("login: not implemented (api-key=%q)", key)
		},
	}
	cmd.Flags().StringVar(&key, "api-key", "", "Relay API key")
	return cmd
}
