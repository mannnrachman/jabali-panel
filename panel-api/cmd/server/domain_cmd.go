package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
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
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			repo := domainRepoFromDB()
			var domains []models.Domain
			var total int64
			var err error

			if userID != "" {
				domains, total, err = repo.ListByUserID(ctx, userID, 0, 1000)
			} else {
				domains, total, err = repo.List(ctx, 0, 1000)
			}

			if err != nil {
				return fmt.Errorf("list domains: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"domains": domains,
					"total":   total,
				})
			}

			if len(domains) == 0 {
				fmt.Println("No domains found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tUSER_ID\tENABLED\tDOC_ROOT")
			for _, d := range domains {
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
	cmd.Flags().StringVar(&userID, "user-id", "", "Filter by user ID")
	return cmd
}

// newDomainCreateCmd creates a new domain.
func newDomainCreateCmd() *cobra.Command {
	var name, userID, docRoot string

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a new domain",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if userID == "" {
				return fmt.Errorf("--user-id is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			domainRepo := domainRepoFromDB()
			userRepo := userRepo()

			// Look up user to derive linux username
			user, err := userRepo.FindByID(ctx, userID)
			if err != nil {
				return fmt.Errorf("fetch user: %w", err)
			}
			if user == nil {
				return fmt.Errorf("user not found: %s", userID)
			}

			// Generate doc_root if not provided
			if docRoot == "" {
				docRoot = "/home/" + deriveLinuxUsername(user.Email) + "/public_html/" + name
			}

			// Create domain in DB
			id := newDomainID()
			domain := &models.Domain{
				ID:        id,
				UserID:    userID,
				Name:      name,
				DocRoot:   docRoot,
				IsEnabled: true,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			if err := domainRepo.Create(ctx, domain); err != nil {
				return fmt.Errorf("create domain: %w", err)
			}

			// Call agent to create the domain
			agentParams := map[string]interface{}{
				"username":     deriveLinuxUsername(user.Email),
				"domain":       name,
				"doc_root":     docRoot,
				"php_version":  "8.3",
			}
			paramsJSON, _ := json.Marshal(agentParams)

			_, err = sharedAgent.Call(ctx, "domain.create", json.RawMessage(paramsJSON))
			if err != nil {
				// Rollback the DB creation on agent error
				_ = domainRepo.Delete(ctx, id)
				return fmt.Errorf("agent create domain: %w", err)
			}

			if jsonOutput {
				return printJSON(domain)
			}

			fmt.Printf("Domain created: %s (ID: %s)\n", name, id)
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
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			domainRepo := domainRepoFromDB()

			domain, err := domainRepo.FindByID(ctx, id)
			if err != nil {
				return fmt.Errorf("fetch domain: %w", err)
			}
			if domain == nil {
				return fmt.Errorf("domain not found: %s", id)
			}

			if domain.IsEnabled {
				fmt.Printf("Domain %s is already enabled\n", domain.Name)
				return nil
			}

			// Call agent
			agentParams := map[string]interface{}{
				"domain": domain.Name,
			}
			paramsJSON, _ := json.Marshal(agentParams)

			_, err = sharedAgent.Call(ctx, "domain.enable", json.RawMessage(paramsJSON))
			if err != nil {
				return fmt.Errorf("agent enable domain: %w", err)
			}

			// Update DB
			domain.IsEnabled = true
			domain.UpdatedAt = time.Now()
			if err := domainRepo.Update(ctx, domain); err != nil {
				return fmt.Errorf("update domain: %w", err)
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
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			domainRepo := domainRepoFromDB()

			domain, err := domainRepo.FindByID(ctx, id)
			if err != nil {
				return fmt.Errorf("fetch domain: %w", err)
			}
			if domain == nil {
				return fmt.Errorf("domain not found: %s", id)
			}

			if !domain.IsEnabled {
				fmt.Printf("Domain %s is already disabled\n", domain.Name)
				return nil
			}

			// Call agent
			agentParams := map[string]interface{}{
				"domain": domain.Name,
			}
			paramsJSON, _ := json.Marshal(agentParams)

			_, err = sharedAgent.Call(ctx, "domain.disable", json.RawMessage(paramsJSON))
			if err != nil {
				return fmt.Errorf("agent disable domain: %w", err)
			}

			// Update DB
			domain.IsEnabled = false
			domain.UpdatedAt = time.Now()
			if err := domainRepo.Update(ctx, domain); err != nil {
				return fmt.Errorf("update domain: %w", err)
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
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			domainRepo := domainRepoFromDB()

			domain, err := domainRepo.FindByID(ctx, id)
			if err != nil {
				return fmt.Errorf("fetch domain: %w", err)
			}
			if domain == nil {
				return fmt.Errorf("domain not found: %s", id)
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

			// Call agent to remove the domain
			agentParams := map[string]interface{}{
				"domain": domain.Name,
			}
			paramsJSON, _ := json.Marshal(agentParams)

			_, err = sharedAgent.Call(ctx, "domain.delete", json.RawMessage(paramsJSON))
			if err != nil {
				return fmt.Errorf("agent delete domain: %w", err)
			}

			// Soft-delete the DB row
			if err := domainRepo.Delete(ctx, id); err != nil {
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
