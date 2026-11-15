package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	apiURL  string
	apiKey  string
	version = "0.1.0-dev"
)

func main() {
	root := &cobra.Command{
		Use:     "relay-cli",
		Short:   "Relay CLI for local webhook development",
		Version: version,
	}
	root.PersistentFlags().StringVar(&apiURL, "api-url", "http://localhost:8080", "Relay API base URL")
	root.PersistentFlags().StringVar(&apiKey, "api-key", "", "API key (otherwise read from ~/.relay/config.json)")

	root.AddCommand(
		newLoginCmd(),
		newEndpointsCmd(),
		newEventsCmd(),
		newListenCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
