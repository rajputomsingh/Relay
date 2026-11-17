package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	relay "github.com/rajputomsingh/relay/sdk-go"
	"github.com/spf13/cobra"
)

func newEndpointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "endpoints",
		Short: "Manage Relay endpoints",
	}
	cmd.AddCommand(
		newEndpointsCreateCmd(),
		newEndpointsListCmd(),
		newEndpointsHealthCmd(),
		newEndpointsUpdateCmd(),
		newEndpointsDeleteCmd(),
	)
	return cmd
}

func newEndpointsCreateCmd() *cobra.Command {
	var (
		name         string
		url          string
		secret       string
		maxAttempts  int
		rateLimitRPS int
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
				MaxAttempts:   maxAttempts,
				RateLimitRPS:  rateLimitRPS,
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
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 5, "Max delivery attempts (1-20)")
	cmd.Flags().IntVar(&rateLimitRPS, "rate-limit-rps", 10, "Rate limit (requests/sec)")
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

func newEndpointsUpdateCmd() *cobra.Command {
	var (
		name         string
		url          string
		secret       string
		maxAttempts  int
		rateLimitRPS int
		enabled      bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			hasChange := changed("name") || changed("url") || changed("secret") ||
				changed("max-attempts") || changed("rate-limit-rps") || changed("enabled")
			if !hasChange {
				return fmt.Errorf("no fields to update; set at least one flag")
			}
			params := relay.UpdateEndpointParams{}
			if changed("name") {
				params.Name = &name
			}
			if changed("url") {
				params.URL = &url
			}
			if changed("secret") {
				params.SigningSecret = &secret
			}
			if changed("max-attempts") {
				params.MaxAttempts = &maxAttempts
			}
			if changed("rate-limit-rps") {
				params.RateLimitRPS = &rateLimitRPS
			}
			if changed("enabled") {
				params.Enabled = &enabled
			}
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			ep, err := client.UpdateEndpoint(context.Background(), args[0], params)
			if err != nil {
				return fmt.Errorf("update endpoint: %w", err)
			}
			fmt.Printf("Updated endpoint %s\n", ep.ID)
			fmt.Printf("  name:    %s\n", ep.Name)
			fmt.Printf("  url:     %s\n", ep.URL)
			fmt.Printf("  enabled: %v\n", ep.Enabled)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Endpoint name")
	cmd.Flags().StringVar(&url, "url", "", "Endpoint URL")
	cmd.Flags().StringVar(&secret, "secret", "", "Signing secret")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 0, "Max delivery attempts (1-20)")
	cmd.Flags().IntVar(&rateLimitRPS, "rate-limit-rps", 0, "Rate limit (requests/sec)")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "Enable/disable the endpoint")
	return cmd
}

func newEndpointsDeleteCmd() *cobra.Command {
	var skipConfirm bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !skipConfirm {
				fmt.Printf("Delete endpoint %s? [y/N] ", args[0])
				var answer string
				if _, err := fmt.Scanln(&answer); err != nil {
					return err
				}
				lower := strings.ToLower(answer)
				if lower != "y" && lower != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			if err := client.DeleteEndpoint(context.Background(), args[0]); err != nil {
				return fmt.Errorf("delete endpoint: %w", err)
			}
			fmt.Printf("Deleted endpoint %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&skipConfirm, "yes", false, "Skip confirmation")
	return cmd
}

func newEndpointsHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health <id>",
		Short: "Show delivery health for an endpoint",
		Long:  "Show delivery health for an endpoint, including statistics for the past hour, consecutive failures, and the latest success and failure timestamps. Missing timestamps are shown as an em-dash (—).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := resolveAPIKey(apiKey)
			if err != nil {
				return err
			}
			client := relay.NewClient(key, relay.WithBaseURL(apiURL))
			health, err := client.GetEndpointHealth(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"success_rate_1h: %g\nattempts_1h: %d\nsuccesses_1h: %d\nfailures_1h: %d\nconsecutive_failures: %d\nlast_success_at: %s\nlast_failure_at: %s\n",
				health.SuccessRate1h, health.Attempts1h, health.Successes1h,
				health.Failures1h, health.ConsecutiveFailures,
				formatHealthTimestamp(health.LastSuccessAt), formatHealthTimestamp(health.LastFailureAt))
			return err
		},
	}
}

func formatHealthTimestamp(timestamp *time.Time) string {
	if timestamp == nil {
		return "—"
	}
	return timestamp.Format(time.RFC3339Nano)
}
