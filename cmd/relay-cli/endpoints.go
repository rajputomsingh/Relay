package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	relay "github.com/rajputomsingh/relay/sdk-go"
	"github.com/spf13/cobra"
)

func newEndpointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "endpoints",
		Short: "Manage Relay endpoints",
	}
	cmd.AddCommand(newEndpointsCreateCmd(), newEndpointsListCmd())
	return cmd
}

func newEndpointsCreateCmd() *cobra.Command {
	var (
		name   string
		url    string
		secret string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an endpoint",
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				return fmt.Errorf("--url is required")
			}
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			ep, err := client.CreateEndpoint(context.Background(), relay.CreateEndpointParams{
				Name:          name,
				URL:           url,
				SigningSecret: secret,
			})
			if err != nil {
				return fmt.Errorf("create endpoint: %w", err)
			}
			fmt.Printf("Created endpoint %s\n", ep.ID)
			fmt.Printf("  name:   %s\n", ep.Name)
			fmt.Printf("  url:    %s\n", ep.URL)
			fmt.Printf("  secret: %v\n", ep.HasSigningSecret)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Endpoint name")
	cmd.Flags().StringVar(&url, "url", "", "Endpoint URL (required)")
	cmd.Flags().StringVar(&secret, "secret", "", "Signing secret")
	return cmd
}

func newEndpointsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List endpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			resp, err := client.ListEndpoints(context.Background())
			if err != nil {
				return fmt.Errorf("list endpoints: %w", err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tNAME\tURL\tENABLED"); err != nil {
				return err
			}
			for _, ep := range resp.Endpoints {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%v\n", ep.ID, ep.Name, ep.URL, ep.Enabled); err != nil {
					return err
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Printf("\n%d endpoint(s)\n", resp.Count)
			return nil
		},
	}
}
