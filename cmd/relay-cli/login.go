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
			if key == "" {
				return fmt.Errorf("--api-key is required")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			cfg.APIKey = key
			if cfg.APIURL == "" {
				cfg.APIURL = apiURL
			}
			if err := saveConfig(cfg); err != nil {
				return err
			}
			path, _ := configPath()
			fmt.Printf("Saved API key to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "api-key", "", "Relay API key")
	return cmd
}
