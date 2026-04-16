package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/clientapi"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
)

func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Manage hosted domains",
	}
	cmd.AddCommand(
		newDomainListCmd(),
		newDomainCreateCmd(),
		newDomainEnableCmd(),
		newDomainDisableCmd(),
		newDomainDeleteCmd(),
	)
	return cmd
}

// newDomainListCmd lists all or filtered domains.
func newDomainListCmd() *cobra.Command {
	var userID string

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List domains",
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			resp, err := client.ListDomains(ctx, 1, 1000)
			if err != nil {
				return fmt.Errorf("list domains: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"domains": resp.Data,
					"total":   resp.Total,
				})
			}

			if len(resp.Data) == 0 {
				fmt.Println("No domains found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tUSER_ID\tENABLED\tDOC_ROOT")
			for _, d := range resp.Data {
				enabled := "no"
				if d.IsEnabled {
					enabled = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					d.ID, d.Name, d.UserID, enabled, d.DocRoot)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&userID, "user-id", "", "Filter by user ID (currently ignored, use API filtering)")
	return cmd
}

// newDomainCreateCmd creates a new domain.
func newDomainCreateCmd() *cobra.Command {
	var name, userID, docRoot string

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a new domain",
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if userID == "" {
				return fmt.Errorf("--user-id is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.CreateDomainRequest{
				Name:    name,
				UserID:  userID,
				DocRoot: docRoot,
			}

			domain, err := client.CreateDomain(ctx, req)
			if err != nil {
				return fmt.Errorf("create domain: %w", err)
			}

			if jsonOutput {
				return printJSON(domain)
			}

			fmt.Printf("Domain created: %s (ID: %s)\n", domain.Name, domain.ID)
			if domain.ProvisionWarning != "" {
				fmt.Printf("Warning: %s\n", domain.ProvisionWarning)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Domain name (required)")
	cmd.Flags().StringVar(&userID, "user-id", "", "User ID (required)")
	cmd.Flags().StringVar(&docRoot, "doc-root", "", "Document root (optional, auto-generated if not provided)")
	return cmd
}

// newDomainEnableCmd enables a domain.
func newDomainEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "enable <domain-id>",
		Short:   "Enable a domain",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.UpdateDomainRequest{
				IsEnabled: boolPtr(true),
			}

			domain, err := client.UpdateDomain(ctx, id, req)
			if err != nil {
				return fmt.Errorf("enable domain: %w", err)
			}

			fmt.Printf("Domain %s enabled\n", domain.Name)
			return nil
		},
	}
}

// newDomainDisableCmd disables a domain.
func newDomainDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "disable <domain-id>",
		Short:   "Disable a domain",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.UpdateDomainRequest{
				IsEnabled: boolPtr(false),
			}

			domain, err := client.UpdateDomain(ctx, id, req)
			if err != nil {
				return fmt.Errorf("disable domain: %w", err)
			}

			fmt.Printf("Domain %s disabled\n", domain.Name)
			return nil
		},
	}
}

// newDomainDeleteCmd deletes a domain (admin only).
func newDomainDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <domain-id>",
		Short:   "Delete a domain",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			// Fetch domain to get the name for confirmation
			domain, err := client.GetDomain(ctx, id)
			if err != nil {
				return fmt.Errorf("fetch domain: %w", err)
			}

			// Confirm deletion unless --force is set
			if !force {
				fmt.Printf("Delete domain %s? (type 'yes' to confirm): ", domain.Name)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			if err := client.DeleteDomain(ctx, id); err != nil {
				return fmt.Errorf("delete domain: %w", err)
			}

			fmt.Printf("Domain %s deleted\n", domain.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

// newDomainID generates a new ULID for domain creation.
func newDomainID() string {
	return ids.NewULID()
}

// deriveLinuxUsername extracts a linux username from an email address.
// For now, use the part before @ or user id.
func deriveLinuxUsername(email string) string {
	if email != "" {
		parts := strings.Split(email, "@")
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return "user"
}
